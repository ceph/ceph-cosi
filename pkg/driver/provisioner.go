/*
Copyright 2021 The Ceph-COSI Authors.
Licensed under the Apache License, Version 2.0 (the "License");
You may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package driver

import (
	"context"
	"errors"
	"os"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/ceph/cosi-driver-ceph/pkg/util/s3client"
	rgwadmin "github.com/ceph/go-ceph/rgw/admin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	cosispec "sigs.k8s.io/container-object-storage-interface-spec"
)

// contains two clients
// 1.) for RGWAdminOps : mainly for user related operations
// 2.) for S3 operations : mainly for bucket related operations
type provisionerServer struct {
	Provisioner string
	Clientset   *kubernetes.Clientset
	KubeConfig  *rest.Config
}

var _ cosispec.ProvisionerServer = &provisionerServer{}

var initializeClients = InitializeClients

func NewProvisionerServer(provisioner string) (cosispec.ProvisionerServer, error) {
	// TODO : use different user this operation
	/*s3Client, err := s3client.NewS3Agent(accessKey, secretKey, rgwEndpoint, true)
	if err != nil {
		return nil, err
	}
	//TODO : add support for TLS endpoint
	rgwAdminClient, err := rgwadmin.New(rgwEndpoint, accessKey, secretKey, nil)
	if err != nil {
		return nil, err
	}*/
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	return &provisionerServer{
		Provisioner: provisioner,
		Clientset:   clientset,
		KubeConfig:  kubeConfig,
	}, nil
}

// ProvisionerCreateBucket is an idempotent method for creating buckets
// It is expected to create the same bucket given a bucketName and protocol
// If the bucket already exists, then it MUST return codes.AlreadyExists
// Return values
//
//	nil -                   Bucket successfully created
//	codes.AlreadyExists -   Bucket already exists. No more retries
//	non-nil err -           Internal error                                [requeue'd with exponential backoff]
func (s *provisionerServer) DriverCreateBucket(ctx context.Context,
	req *cosispec.DriverCreateBucketRequest) (*cosispec.DriverCreateBucketResponse, error) {
	klog.InfoS("Using ceph rgw to create Backend Bucket")

	bucketName := req.GetName()
	klog.V(3).InfoS("Creating Bucket", "name", bucketName)

	parameters := req.GetParameters()

	s3Client, _, err := initializeClients(ctx, s.Clientset, parameters)
	if err != nil {
		klog.ErrorS(err, "failed to initialize clients")
		return nil, status.Error(codes.Internal, "failed to initialize clients")
	}

	err = s3Client.CreateBucket(bucketName)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			klog.InfoS("DEBUG: after s3 call", "ok", ok, "aerr", aerr)
			switch aerr.Code() {
			case s3.ErrCodeBucketAlreadyExists:
				klog.InfoS("bucket already exists", "name", bucketName)
				return nil, status.Error(codes.AlreadyExists, "bucket already exists")
			case s3.ErrCodeBucketAlreadyOwnedByYou:
				klog.InfoS("bucket already owned by you", "name", bucketName)
				return nil, status.Error(codes.AlreadyExists, "bucket already owned by you")
			}
		}
		klog.ErrorS(err, "failed to create bucket", "bucketName", bucketName)
		return nil, status.Error(codes.Internal, "failed to create bucket")
	}
	klog.InfoS("Successfully created Backend Bucket", "bucketName", bucketName)

	return &cosispec.DriverCreateBucketResponse{
		BucketId: bucketName,
	}, nil
}

func (s *provisionerServer) DriverDeleteBucket(ctx context.Context,
	req *cosispec.DriverDeleteBucketRequest) (*cosispec.DriverDeleteBucketResponse, error) {

	klog.InfoS("Backend Bucket is not yet deleted", "id", req.GetBucketId())

	return &cosispec.DriverDeleteBucketResponse{}, nil
}

func (s *provisionerServer) DriverGrantBucketAccess(ctx context.Context,
	req *cosispec.DriverGrantBucketAccessRequest) (*cosispec.DriverGrantBucketAccessResponse, error) {
	// TODO : validate below details, Authenticationtype, Parameters
	userName := req.GetName()
	bucketName := req.GetBucketId()
	klog.InfoS("Granting user accessPolicy to bucket", "userName", userName, "bucketName", bucketName)
	parameters := req.GetParameters()

	s3Client, rgwAdminClient, err := initializeClients(ctx, s.Clientset, parameters)
	if err != nil {
		klog.ErrorS(err, "failed to initialize clients")
		return nil, status.Error(codes.Internal, "failed to initialize clients")
	}

	user, err := rgwAdminClient.CreateUser(ctx, rgwadmin.User{
		ID:          userName,
		DisplayName: userName,
	})

	// TODO : Do we need fail for UserErrorExists, or same account can have multiple BAR
	if err != nil && !errors.Is(err, rgwadmin.ErrUserExists) {
		klog.ErrorS(err, "failed to create user")
		return nil, status.Error(codes.Internal, "User creation failed")
	}

	policy, err := s3Client.GetBucketPolicy(bucketName)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() != "NoSuchBucketPolicy" {
			return nil, status.Error(codes.Internal, "fetching policy failed")
		}
	}

	statement := s3client.NewPolicyStatement().
		WithSID(userName).
		ForPrincipals(userName).
		ForResources(bucketName).
		ForSubResources(bucketName).
		Allows().
		Actions(s3client.AllowedActions...)
	if policy == nil {
		policy = s3client.NewBucketPolicy(*statement)
	} else {
		policy = policy.ModifyBucketPolicy(*statement)
	}
	_, err = s3Client.PutBucketPolicy(bucketName, *policy)
	if err != nil {
		klog.ErrorS(err, "failed to set policy")
		return nil, status.Error(codes.Internal, "failed to set policy")
	}

	// TODO : limit the bucket count for this user to 0

	// Below response if not final, may change in future
	return &cosispec.DriverGrantBucketAccessResponse{
		AccountId:   userName,
		Credentials: fetchUserCredentials(user, rgwAdminClient.Endpoint, ""),
	}, nil
}

func (s *provisionerServer) DriverRevokeBucketAccess(ctx context.Context,
	req *cosispec.DriverRevokeBucketAccessRequest) (*cosispec.DriverRevokeBucketAccessResponse, error) {

	// TODO : instead of deleting user, revoke its permission and delete only if no more bucket attached to it
	klog.InfoS("User is actual not removed from backend", "id", req.GetAccountId())

	return &cosispec.DriverRevokeBucketAccessResponse{}, nil
}

func fetchUserCredentials(user rgwadmin.User, endpoint string, region string) map[string]*cosispec.CredentialDetails {
	s3Keys := make(map[string]string)
	s3Keys["accessKeyID"] = user.Keys[0].AccessKey
	s3Keys["accessSecretKey"] = user.Keys[0].SecretKey
	s3Keys["endpoint"] = endpoint
	s3Keys["region"] = region
	creds := &cosispec.CredentialDetails{
		Secrets: s3Keys,
	}
	credDetails := make(map[string]*cosispec.CredentialDetails)
	credDetails["s3"] = creds
	return credDetails
}

func InitializeClients(ctx context.Context, clientset *kubernetes.Clientset, parameters map[string]string) (*s3client.S3Agent, *rgwadmin.API, error) {
	objectStoreUserSecretName := parameters["ObjectStoreUserSecretName"]
	namespace := os.Getenv("POD_NAMESPACE")
	if parameters["ObjectStoreNamespace"] != "" {
		namespace = parameters["ObjectStoreNamespace"]
	}
	if objectStoreUserSecretName == "" || namespace == "" {
		return nil, nil, status.Error(codes.InvalidArgument, "ObjectStoreUserSecretName and Namespace is required")
	}

	objectStoreUserSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, objectStoreUserSecretName, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "failed to get object store user secret")
		return nil, nil, status.Error(codes.Internal, "failed to get object store user secret")
	}

	accessKey, secretKey, rgwEndpoint, _, err := fetchParameters(objectStoreUserSecret.Data)
	if err != nil {
		return nil, nil, err
	}

	// TODO : validate endpoint and support TLS certs

	rgwAdminClient, err := rgwadmin.New(rgwEndpoint, accessKey, secretKey, nil)
	if err != nil {
		klog.ErrorS(err, "failed to create rgw admin client")
		return nil, nil, status.Error(codes.Internal, "failed to create rgw admin client")
	}
	s3Client, err := s3client.NewS3Agent(accessKey, secretKey, rgwEndpoint, nil, true)
	if err != nil {
		klog.ErrorS(err, "failed to create s3 client")
		return nil, nil, status.Error(codes.Internal, "failed to create s3 client")
	}
	return s3Client, rgwAdminClient, nil
}

func fetchParameters(parameters map[string][]byte) (string, string, string, string, error) {

	accessKey := string(parameters["AccessKey"])
	secretKey := string(parameters["SecretKey"])
	endPoint := string(parameters["Endpoint"])
	if endPoint == "" || accessKey == "" || secretKey == "" {
		return "", "", "", "", status.Error(codes.InvalidArgument, "endpoint, accessKeyID and secretKey are required")
	}
	tlsCert := string(parameters["SSLCertSecretName"])

	return accessKey, secretKey, endPoint, tlsCert, nil
}
