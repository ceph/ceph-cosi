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
	"slices"
	"strings"

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
	"k8s.io/utils/ptr"
	bucketclientset "sigs.k8s.io/container-object-storage-interface-api/client/clientset/versioned"
	cosispec "sigs.k8s.io/container-object-storage-interface-spec"
)

type Parameters struct {
	Parent    string
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	TLSCert   []byte
}

// contains two clients
// 1.) for RGWAdminOps : mainly for user related operations
// 2.) for S3 operations : mainly for bucket related operations
type provisionerServer struct {
	Provisioner     string
	Clientset       *kubernetes.Clientset
	KubeConfig      *rest.Config
	BucketClientset bucketclientset.Interface
}

var _ cosispec.ProvisionerServer = &provisionerServer{}

var (
	fetchParameters   = FetchParameters
	initializeClients = InitializeClients
)

func NewProvisionerServer(provisioner string) (cosispec.ProvisionerServer, error) {
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	bucketClientset, err := bucketclientset.NewForConfig(kubeConfig)
	if err != nil {
		return nil, err
	}

	return &provisionerServer{
		Provisioner:     provisioner,
		Clientset:       clientset,
		KubeConfig:      kubeConfig,
		BucketClientset: bucketClientset,
	}, nil
}

func FetchParameters(ctx context.Context, clientset *kubernetes.Clientset, req map[string]string) (*Parameters, error) {
	name := req["objectStoreUserSecretName"]
	namespace := os.Getenv("POD_NAMESPACE")
	if req["objectStoreUserSecretNamespace"] != "" {
		namespace = req["objectStoreUserSecretNamespace"]
	}
	if name == "" || namespace == "" {
		return nil, status.Error(codes.InvalidArgument, "objectStoreUserSecretName and objectStoreUserSecretNamespace is required")
	}

	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "failed to get object store user secret")
		return nil, status.Error(codes.Internal, "failed to get object store user secret")
	}

	data := secret.Data
	parameters := &Parameters{
		Endpoint:  string(data["Endpoint"]),
		Region:    string(data["Region"]),
		AccessKey: string(data["AccessKey"]),
		SecretKey: string(data["SecretKey"]),
		Parent:    string(data["Parent"]),
	}
	if parameters.Endpoint == "" || parameters.AccessKey == "" || parameters.SecretKey == "" {
		return nil, status.Error(codes.InvalidArgument, "endpoint, accessKeyID and secretKey are required")
	}

	sslCertName := string(data["SSLCertSecretName"])
	if sslCertName != "" {
		secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, sslCertName, metav1.GetOptions{})
		if err != nil {
			klog.ErrorS(err, "failed to get object store ssl cert")
			return nil, status.Error(codes.Internal, "failed to get object store ssl cert")
		}

		parameters.TLSCert = secret.Data["tls.crt"]
	}

	return parameters, nil
}

func InitializeClients(ctx context.Context, clientset *kubernetes.Clientset, parameters *Parameters) (*s3client.S3Agent, *rgwadmin.API, error) {
	klog.V(5).InfoS("initializing clients", "endpoint", parameters.Endpoint, "access_key", parameters.AccessKey)

	// TODO : validate endpoint and support TLS certs
	rgwAdminClient, err := rgwadmin.New(parameters.Endpoint, parameters.AccessKey, parameters.SecretKey, nil)
	if err != nil {
		klog.ErrorS(err, "failed to create rgw admin client")
		return nil, nil, status.Error(codes.Internal, "failed to create rgw admin client")
	}

	s3Client, err := s3client.NewS3Agent(parameters.AccessKey, parameters.SecretKey, parameters.Endpoint, nil, true)
	if err != nil {
		klog.ErrorS(err, "failed to create s3 client")
		return nil, nil, status.Error(codes.Internal, "failed to create s3 client")
	}
	return s3Client, rgwAdminClient, nil
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
	klog.V(5).Infof("req %v", req)

	bucketName := req.GetName()
	if bucketName == "" {
		return nil, status.Error(codes.InvalidArgument, "bucket name is required")
	}

	klog.V(3).InfoS("Creating Bucket", "name", bucketName)

	parameters, err := fetchParameters(ctx, s.Clientset, req.GetParameters())
	if err != nil {
		return nil, err
	}

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
	klog.V(5).Infof("req %v", req)
	bucketName := req.GetBucketId()
	klog.V(3).InfoS("Deleting Bucket", "name", bucketName)
	bucket, err := s.BucketClientset.ObjectstorageV1alpha1().Buckets().Get(ctx, bucketName, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "failed to get bucket", "bucketName", bucketName)
		return nil, status.Error(codes.Internal, "failed to get bucket")
	}

	parameters, err := fetchParameters(ctx, s.Clientset, bucket.Spec.Parameters)
	if err != nil {
		return nil, err
	}

	s3Client, _, err := initializeClients(ctx, s.Clientset, parameters)
	if err != nil {
		klog.ErrorS(err, "failed to initialize clients")
		return nil, status.Error(codes.Internal, "failed to initialize clients")
	}

	_, err = s3Client.DeleteBucket(bucketName)
	if err != nil {
		klog.ErrorS(err, "failed to delete bucket", "bucketName", bucketName)
		return nil, status.Error(codes.Internal, "failed to delete bucket")
	}
	klog.InfoS("Successfully deleted Backend Bucket", "bucketName", bucketName)
	return &cosispec.DriverDeleteBucketResponse{}, nil
}

func (s *provisionerServer) DriverGrantBucketAccess(ctx context.Context, req *cosispec.DriverGrantBucketAccessRequest) (*cosispec.DriverGrantBucketAccessResponse, error) {
	// TODO : validate below details, Authenticationtype, Parameters
	bucketName := req.GetBucketId()
	klog.V(5).Infof("req %v", req)
	klog.InfoS("Granting user accessPolicy to bucket", "userName", req.GetName(), "bucketName", bucketName)

	params, err := fetchParameters(ctx, s.Clientset, req.GetParameters())
	if err != nil {
		return nil, err
	}

	s3Client, rgwAdminClient, err := initializeClients(ctx, s.Clientset, params)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to initialize clients")
	}

	policy, err := s3Client.GetBucketPolicy(bucketName)
	if err != nil {
		switch awsErrorCode(err) {
		case "NoSuchBucketPolicy":
			// noop
		case "NoSuchBucket":
			return nil, status.Error(codes.NotFound, "bucket not found")
		default:
			klog.ErrorS(err, "get bucket policy", "userName", req.Name, "bucketName", bucketName)
			return nil, status.Error(codes.Internal, "fetching policy failed")
		}
	}

	var key rgwadmin.UserKeySpec
	switch {
	case params.Parent == "":
		key, err = createUser(ctx, rgwAdminClient, req.Name)
	default:
		key, err = createSubUser(ctx, rgwAdminClient, params.Parent, req.Name)
	}
	if err != nil {
		return nil, err
	}

	response := marshalBucketAccessResponse(key, params)
	statement := s3client.NewPolicyStatement().
		WithSID(response.AccountId).
		ForPrincipals(params.Parent).
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
	return response, nil
}

func (s *provisionerServer) DriverRevokeBucketAccess(ctx context.Context,
	req *cosispec.DriverRevokeBucketAccessRequest) (*cosispec.DriverRevokeBucketAccessResponse, error) {
	klog.V(5).Infof("req %v", req)
	bucketName := req.GetBucketId()
	bucket, err := s.BucketClientset.ObjectstorageV1alpha1().Buckets().Get(ctx, bucketName, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "failed to get bucket", "bucketName", bucketName)
		return nil, status.Error(codes.Internal, "failed to get bucket")
	}

	parameters, err := fetchParameters(ctx, s.Clientset, bucket.Spec.Parameters)
	if err != nil {
		return nil, err
	}

	_, rgwAdminClient, err := initializeClients(ctx, s.Clientset, parameters)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to initialize clients")
	}

	userName := req.GetAccountId()

	// TODO : instead of deleting user, revoke its permission and delete only if no more bucket attached to it
	switch {
	case parameters.Parent == "":
		err = rgwAdminClient.RemoveUser(ctx, rgwadmin.User{ID: userName})
		if err != nil && !errors.Is(err, rgwadmin.ErrNoSuchUser) {
			klog.ErrorS(err, "failed to remove user")
			return nil, status.Error(codes.Internal, "failed to remove user")
		}
	default:
		parent := rgwadmin.User{ID: parameters.Parent}
		err = rgwAdminClient.RemoveSubuser(ctx, parent, rgwadmin.SubuserSpec{
			Name: userName,
		})
		if err != nil && strings.HasPrefix("NoSuchSubUser", err.Error()) {
			klog.ErrorS(err, "failed to remove subuser")
			return nil, status.Error(codes.Internal, "failed to remove subuser")
		}
	}

	return &cosispec.DriverRevokeBucketAccessResponse{}, nil
}

func createUser(ctx context.Context, client *rgwadmin.API, userName string) (rgwadmin.UserKeySpec, error) {
	user, err := client.CreateUser(ctx, rgwadmin.User{
		ID:          userName,
		DisplayName: userName,
	})
	if errors.Is(err, rgwadmin.ErrUserExists) {
		user, err = getUser(ctx, client, userName)
	}

	if err != nil {
		klog.ErrorS(err, "failed to create user", "userName", userName)
		return rgwadmin.UserKeySpec{}, status.Error(codes.Internal, "User creation failed")
	}

	return user.Keys[0], nil
}

func createSubUser(ctx context.Context, client *rgwadmin.API, parent, userName string) (rgwadmin.UserKeySpec, error) {
	parentUser := rgwadmin.User{ID: parent}
	err := client.CreateSubuser(ctx, parentUser, rgwadmin.SubuserSpec{
		Name:    userName,
		Access:  rgwadmin.SubuserAccessFull,
		KeyType: ptr.To("s3"),
	})
	if err != nil && !errors.Is(err, rgwadmin.ErrSubuserExists) {
		klog.ErrorS(err, "failed to create subuser", "parent", parent)
		return rgwadmin.UserKeySpec{}, status.Error(codes.Internal, "Subuser creation failed")
	}

	user, err := getUser(ctx, client, parent)
	if err != nil {
		return rgwadmin.UserKeySpec{}, err
	}

	userName = parent + ":" + userName
	i := slices.IndexFunc(user.Keys, func(key rgwadmin.UserKeySpec) bool {
		return key.User == userName
	})
	if i == -1 {
		klog.ErrorS(errors.New("lookup key"), "key not found", "userName", userName)
		return rgwadmin.UserKeySpec{}, status.Error(codes.NotFound, "Key not found in user object")
	}

	return user.Keys[i], nil
}

func getUser(ctx context.Context, client *rgwadmin.API, userName string) (rgwadmin.User, error) {
	user, err := client.GetUser(ctx, rgwadmin.User{ID: userName})
	switch {
	case errors.Is(err, rgwadmin.ErrNoSuchUser):
		return rgwadmin.User{}, status.Error(codes.NotFound, "User not found")
	case err != nil:
		klog.ErrorS(err, "failed to get user", "userName", userName)
		return rgwadmin.User{}, status.Error(codes.Internal, "Get user failed")
	default:
		return user, nil
	}
}

func marshalBucketAccessResponse(key rgwadmin.UserKeySpec, params *Parameters) *cosispec.DriverGrantBucketAccessResponse {
	s3Keys := map[string]string{
		"endpoint":        params.Endpoint,
		"region":          params.Region,
		"accessKeyID":     key.AccessKey,
		"accessSecretKey": key.AccessKey,
	}

	return &cosispec.DriverGrantBucketAccessResponse{
		AccountId: key.User,
		Credentials: map[string]*cosispec.CredentialDetails{
			"s3": {
				Secrets: s3Keys,
			},
		},
	}
}

func awsErrorCode(err error) string {
	var aerr awserr.Error
	if !errors.As(err, &aerr) {
		return ""
	}

	return aerr.Code()
}
