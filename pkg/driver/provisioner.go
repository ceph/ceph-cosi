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

	"github.com/ceph/cosi-driver-ceph/pkg/util/s3client"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	rgwadmin "github.com/ceph/go-ceph/rgw/admin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	cosispec "sigs.k8s.io/container-object-storage-interface/proto"
)

// contains two clients
// 1.) for RGWAdminOps : mainly for user related operations
// 2.) for S3 operations : mainly for bucket related operations
type provisionerServer struct {
	cosispec.UnimplementedProvisionerServer
	Provisioner string
	Clientset   kubernetes.Interface
	KubeConfig  *rest.Config
}

var _ cosispec.ProvisionerServer = &provisionerServer{}

var initializeClients = InitializeClients

func NewProvisionerServer(provisioner string) (cosispec.ProvisionerServer, error) {
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

func (s *provisionerServer) DriverGenerateBucketId(ctx context.Context,
	req *cosispec.DriverGenerateBucketIdRequest) (*cosispec.DriverGenerateBucketIdResponse, error) {
	klog.V(5).InfoS("DriverGenerateBucketId", "req", req)
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "bucket name cannot be empty")
	}
	return &cosispec.DriverGenerateBucketIdResponse{
		BucketId: name,
	}, nil
}

func (s *provisionerServer) DriverCreateBucket(ctx context.Context,
	req *cosispec.DriverCreateBucketRequest) (*cosispec.DriverCreateBucketResponse, error) {
	klog.V(5).InfoS("DriverCreateBucket", "req", req)

	bucketID := req.GetBucketId()
	if bucketID == "" {
		return nil, status.Error(codes.InvalidArgument, "bucket ID cannot be empty")
	}
	klog.V(3).InfoS("Creating Bucket", "bucketID", bucketID)

	parameters := req.GetParameters()

	s3Client, rgwAdminClient, err := initializeClients(ctx, s.Clientset, parameters)
	if err != nil {
		klog.ErrorS(err, "failed to initialize clients")
		return nil, status.Error(codes.Internal, "failed to initialize clients")
	}

	err = s3Client.CreateBucket(bucketID)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			klog.InfoS("DEBUG: after s3 call", "ok", ok, "aerr", aerr)
			switch aerr.Code() {
			case s3.ErrCodeBucketAlreadyExists:
				klog.InfoS("bucket already exists", "name", bucketID)
				return nil, status.Error(codes.AlreadyExists, "bucket already exists")
			case s3.ErrCodeBucketAlreadyOwnedByYou:
				klog.InfoS("bucket already owned by you", "name", bucketID)
				return &cosispec.DriverCreateBucketResponse{
					Protocols: &cosispec.ObjectProtocolAndBucketInfo{
						S3: &cosispec.S3BucketInfo{
							BucketId: bucketID,
							Endpoint: rgwAdminClient.Endpoint,
							AddressingStyle: &cosispec.S3AddressingStyle{
								Style: cosispec.S3AddressingStyle_PATH,
							},
						},
					},
				}, nil
			}
		}
		klog.ErrorS(err, "failed to create bucket", "bucketID", bucketID)
		return nil, status.Error(codes.Internal, "failed to create bucket")
	}
	klog.InfoS("Successfully created Backend Bucket", "bucketID", bucketID)

	return &cosispec.DriverCreateBucketResponse{
		Protocols: &cosispec.ObjectProtocolAndBucketInfo{
			S3: &cosispec.S3BucketInfo{
				BucketId: bucketID,
				Endpoint: rgwAdminClient.Endpoint,
				AddressingStyle: &cosispec.S3AddressingStyle{
					Style: cosispec.S3AddressingStyle_PATH,
				},
			},
		},
	}, nil
}

func (s *provisionerServer) DriverGetBucket(ctx context.Context,
	req *cosispec.DriverGetBucketRequest) (*cosispec.DriverGetBucketResponse, error) {
	klog.V(5).InfoS("DriverGetBucket", "req", req)

	bucketID := req.GetBucketId()
	if bucketID == "" {
		return nil, status.Error(codes.InvalidArgument, "bucket ID cannot be empty")
	}
	parameters := req.GetParameters()

	s3Client, rgwAdminClient, err := initializeClients(ctx, s.Clientset, parameters)
	if err != nil {
		klog.ErrorS(err, "failed to initialize clients")
		return nil, status.Error(codes.Internal, "failed to initialize clients")
	}

	_, err = s3Client.Client.HeadBucket(&s3.HeadBucketInput{
		Bucket: &bucketID,
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() == s3.ErrCodeNoSuchBucket || aerr.Code() == "NoSuchBucket" || aerr.Code() == "NotFound" {
				return nil, status.Error(codes.NotFound, "bucket not found")
			}
		}
		klog.ErrorS(err, "failed to get bucket", "bucketID", bucketID)
		return nil, status.Error(codes.Internal, "failed to get bucket")
	}

	return &cosispec.DriverGetBucketResponse{
		Protocols: &cosispec.ObjectProtocolAndBucketInfo{
			S3: &cosispec.S3BucketInfo{
				BucketId: bucketID,
				Endpoint: rgwAdminClient.Endpoint,
				AddressingStyle: &cosispec.S3AddressingStyle{
					Style: cosispec.S3AddressingStyle_PATH,
				},
			},
		},
	}, nil
}

func (s *provisionerServer) DriverDeleteBucket(ctx context.Context,
	req *cosispec.DriverDeleteBucketRequest) (*cosispec.DriverDeleteBucketResponse, error) {
	klog.V(5).InfoS("DriverDeleteBucket", "req", req)
	bucketName := req.GetBucketId()
	if bucketName == "" {
		return nil, status.Error(codes.InvalidArgument, "bucket ID cannot be empty")
	}
	klog.V(3).InfoS("Deleting Bucket", "name", bucketName)

	parameters := req.GetParameters()
	s3Client, _, err := initializeClients(ctx, s.Clientset, parameters)
	if err != nil {
		klog.ErrorS(err, "failed to initialize clients")
		return nil, status.Error(codes.Internal, "failed to initialize clients")
	}

	_, err = s3Client.DeleteBucket(bucketName)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() == s3.ErrCodeNoSuchBucket || aerr.Code() == "NoSuchBucket" || aerr.Code() == "NotFound" {
				klog.InfoS("bucket already deleted", "bucketName", bucketName)
				return &cosispec.DriverDeleteBucketResponse{}, nil
			}
		}
		klog.ErrorS(err, "failed to delete bucket", "bucketName", bucketName)
		return nil, status.Error(codes.Internal, "failed to delete bucket")
	}
	klog.InfoS("Successfully deleted Backend Bucket", "bucketName", bucketName)
	return &cosispec.DriverDeleteBucketResponse{}, nil
}

func (s *provisionerServer) DriverGrantBucketAccess(ctx context.Context,
	req *cosispec.DriverGrantBucketAccessRequest) (*cosispec.DriverGrantBucketAccessResponse, error) {
	userName := req.GetAccountName()
	if userName == "" {
		return nil, status.Error(codes.InvalidArgument, "account name cannot be empty")
	}
	if len(req.GetBuckets()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one bucket must be requested")
	}
	for _, b := range req.GetBuckets() {
		if b.GetBucketId() == "" {
			return nil, status.Error(codes.InvalidArgument, "bucket ID cannot be empty")
		}
	}

	klog.V(5).InfoS("DriverGrantBucketAccess", "req", req)
	klog.InfoS("Granting user accessPolicy to bucket", "userName", userName)
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

	if errors.Is(err, rgwadmin.ErrUserExists) {
		// A previous grant attempt may have created the user before failing.
		// CreateUser returns an empty User on error, so retrieve its credentials.
		user, err = rgwAdminClient.GetUser(ctx, rgwadmin.User{ID: userName})
		if err != nil {
			klog.ErrorS(err, "failed to get existing user")
			return nil, status.Error(codes.Internal, "failed to get existing user")
		}
	} else if err != nil {
		klog.ErrorS(err, "failed to create user")
		return nil, status.Error(codes.Internal, "User creation failed")
	}

	if len(user.Keys) == 0 || user.Keys[0].AccessKey == "" || user.Keys[0].SecretKey == "" {
		klog.ErrorS(nil, "user has no usable S3 credentials", "userName", userName)
		return nil, status.Error(codes.Internal, "user has no usable S3 credentials")
	}

	bucketInfos := make([]*cosispec.DriverGrantBucketAccessResponse_BucketInfo, 0, len(req.GetBuckets()))
	for _, b := range req.GetBuckets() {
		bucketName := b.GetBucketId()
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

		bucketInfos = append(bucketInfos, &cosispec.DriverGrantBucketAccessResponse_BucketInfo{
			BucketId: bucketName,
			BucketInfo: &cosispec.ObjectProtocolAndBucketInfo{
				S3: &cosispec.S3BucketInfo{
					BucketId: bucketName,
					Endpoint: rgwAdminClient.Endpoint,
					AddressingStyle: &cosispec.S3AddressingStyle{
						Style: cosispec.S3AddressingStyle_PATH,
					},
				},
			},
		})
	}

	return &cosispec.DriverGrantBucketAccessResponse{
		AccountId:   userName,
		Buckets:     bucketInfos,
		Credentials: fetchUserCredentials(user),
	}, nil
}

func (s *provisionerServer) DriverRevokeBucketAccess(ctx context.Context,
	req *cosispec.DriverRevokeBucketAccessRequest) (*cosispec.DriverRevokeBucketAccessResponse, error) {
	klog.V(5).InfoS("DriverRevokeBucketAccess", "req", req)
	userName := req.GetAccountId()
	if userName == "" {
		return nil, status.Error(codes.InvalidArgument, "account ID cannot be empty")
	}

	parameters := req.GetParameters()
	_, rgwAdminClient, err := initializeClients(ctx, s.Clientset, parameters)
	if err != nil {
		klog.ErrorS(err, "failed to initialize clients")
		return nil, status.Error(codes.Internal, "failed to initialize clients")
	}

	// TODO : instead of deleting user, revoke its permission and delete only if no more bucket attached to it
	err = rgwAdminClient.RemoveUser(ctx, rgwadmin.User{ID: userName})
	if err != nil && !errors.Is(err, rgwadmin.ErrNoSuchUser) {
		klog.ErrorS(err, "failed to delete user")
		return nil, status.Error(codes.Internal, "failed to delete user")
	}
	return &cosispec.DriverRevokeBucketAccessResponse{}, nil
}

func fetchUserCredentials(user rgwadmin.User) *cosispec.CredentialInfo {
	return &cosispec.CredentialInfo{
		S3: &cosispec.S3CredentialInfo{
			AccessKeyId:     user.Keys[0].AccessKey,
			AccessSecretKey: user.Keys[0].SecretKey,
		},
	}
}

func InitializeClients(ctx context.Context, clientset kubernetes.Interface, parameters map[string]string) (*s3client.S3Agent, *rgwadmin.API, error) {
	klog.V(5).Infof("Initializing clients %v", parameters)

	objectStoreUserSecretName, namespace, err := fetchSecretNameAndNamespace(parameters)
	if err != nil {
		return nil, nil, err
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

func fetchParameters(secretData map[string][]byte) (string, string, string, string, error) {
	accessKey := string(secretData["AccessKey"])
	secretKey := string(secretData["SecretKey"])
	endPoint := string(secretData["Endpoint"])
	if endPoint == "" || accessKey == "" || secretKey == "" {
		return "", "", "", "", status.Error(codes.InvalidArgument, "endpoint, accessKeyID and secretKey are required")
	}
	tlsCert := string(secretData["SSLCertSecretName"])

	return accessKey, secretKey, endPoint, tlsCert, nil
}

func fetchSecretNameAndNamespace(parameters map[string]string) (string, string, error) {
	secretName := parameters["objectStoreUserSecretName"]
	namespace := os.Getenv("POD_NAMESPACE")
	if parameters["objectStoreUserSecretNamespace"] != "" {
		namespace = parameters["objectStoreUserSecretNamespace"]
	}
	if secretName == "" || namespace == "" {
		return "", "", status.Error(codes.InvalidArgument, "objectStoreUserSecretName and Namespace is required")
	}

	return secretName, namespace, nil
}
