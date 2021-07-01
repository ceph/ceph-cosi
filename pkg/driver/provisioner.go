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
	"fmt"

	"github.com/aws/aws-sdk-go/aws/awserr"
	s3cli "github.com/ceph/cosi-driver-ceph/pkg/util/s3client"
	rgwadmin "github.com/ceph/go-ceph/rgw/admin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	cosi "sigs.k8s.io/container-object-storage-interface-spec"
)

// contains two clients
// 1.) for RGWAdminOps : mainly for user related operations
// 2.) for S3 operations : mainly for bucket related operations
type ProvisionerServer struct {
	provisioner    string
	s3Client       *s3cli.S3Agent
	rgwAdminClient *rgwadmin.API
}

// ProvisionerCreateBucket is an idempotent method for creating buckets
// It is expected to create the same bucket given a bucketName and protocol
// If the bucket already exists, then it MUST return codes.AlreadyExists
// Return values
//    nil -                   Bucket successfully created
//    codes.AlreadyExists -   Bucket already exists. No more retries
//    non-nil err -           Internal error                                [requeue'd with exponential backoff]
func (s *ProvisionerServer) ProvisionerCreateBucket(ctx context.Context,
	req *cosi.ProvisionerCreateBucketRequest) (*cosi.ProvisionerCreateBucketResponse, error) {
	klog.Info("Using ceph rgw to create Backend Bucket")
	protocol := req.GetProtocol()
	if protocol == nil {
		klog.ErrorS(errNilProtocol, "Protocol is nil")
		return nil, status.Error(codes.InvalidArgument, "Protocol is nil")
	}
	s3 := protocol.GetS3()
	if s3 == nil {
		klog.ErrorS(errs3ProtocolMissing, "S3 protocol is missing, only S3 is supported")
		return nil, status.Error(codes.InvalidArgument, "only S3 protocol supported")
	}
	//TODO : validate S3 protocol defined, check points valid rgwendpoint, v4 signature check etc
	bucketName := req.GetName()
	klog.V(3).InfoS("Creating Bucket", "name", bucketName)

	err := s.s3Client.CreateBucket(bucketName)
	if err != nil {
		// Check to see if the bucket already exists by above api
		klog.ErrorS(err, "failed to create bucket %q", bucketName)
		return nil, status.Error(codes.Internal, "failed to create bucket")
	}
	klog.Infof("Successfully created Backend Bucket %q", bucketName)

	return &cosi.ProvisionerCreateBucketResponse{
		BucketId: bucketName,
	}, nil
}

func (s *ProvisionerServer) ProvisionerDeleteBucket(ctx context.Context,
	req *cosi.ProvisionerDeleteBucketRequest) (*cosi.ProvisionerDeleteBucketResponse, error) {
	klog.Infof("Deleting bucket %q", req.GetBucketId())
	if _, err := s.s3Client.DeleteBucket(req.GetBucketId()); err != nil {
		klog.ErrorS(err, "failed to delete bucket %q", req.GetBucketId())
		return nil, status.Error(codes.Internal, "failed to delete bucket")
	}
	klog.Infof("Successfully deleted Bucket %q", req.GetBucketId())

	return &cosi.ProvisionerDeleteBucketResponse{}, nil
}

func (s *ProvisionerServer) ProvisionerGrantBucketAccess(ctx context.Context,
	req *cosi.ProvisionerGrantBucketAccessRequest) (*cosi.ProvisionerGrantBucketAccessResponse, error) {
	// TODO : validate below details, if accountname is empty create internal user
	userName := req.GetAccountName()
	bucketName := req.GetBucketId()
	accessPolicy := req.GetAccessPolicy()
	klog.Info("Granting user %q the policy %q to bucket %q", userName, bucketName, accessPolicy)
	user, err := s.rgwAdminClient.CreateUser(ctx, rgwadmin.User{
		ID:          userName,
		DisplayName: userName,
	})
	// TODO : Do we need fail for UserErrorExists, or same account can have multiple BAR
	if err != nil && !errors.Is(err, rgwadmin.ErrUserExists) {
		klog.Error("failed to create user", err)
		return nil, status.Error(codes.Internal, "User creation failed")
	}

	// TODO : Handle access policy in request, currently granting all perms to this user
	policy, err := s.s3Client.GetBucketPolicy(bucketName)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() != "NoSuchBucketPolicy" {
			return nil, status.Error(codes.Internal, "fetching policy failed")
		}
	}

	statement := s3cli.NewPolicyStatement().
		WithSID(userName).
		ForPrincipals(userName).
		ForResources(bucketName).
		ForSubResources(bucketName).
		Allows().
		Actions(s3cli.AllowedActions...)
	if policy == nil {
		policy = s3cli.NewBucketPolicy(*statement)
	} else {
		policy = policy.ModifyBucketPolicy(*statement)
	}
	_, err = s.s3Client.PutBucketPolicy(bucketName, *policy)
	if err != nil {
		klog.Error("failed to set policy", err)
		return nil, status.Error(codes.Internal, "failed to set policy")
	}

	// TODO : limit the bucket count for this user to 0

	// Below response if not final, may change in future
	return &cosi.ProvisionerGrantBucketAccessResponse{
		AccountId:   userName,
		Credentials: fetchUserCredentials(user),
	}, nil
}

func (s *ProvisionerServer) ProvisionerRevokeBucketAccess(ctx context.Context,
	req *cosi.ProvisionerRevokeBucketAccessRequest) (*cosi.ProvisionerRevokeBucketAccessResponse, error) {

	// TODO : instead of deleting user, revoke its permission and delete only if no more bucket attached to it
	klog.Infof("Deleting user %q", req.GetAccountId())
	if err := s.rgwAdminClient.RemoveUser(context.Background(), rgwadmin.User{
		ID:          req.GetAccountId(),
		DisplayName: req.GetAccountId(),
	}); err != nil {
		klog.Error("failed to Revoke Bucket Access")
		return nil, status.Error(codes.Internal, "failed to Revoke Bucket Access")
	}
	return &cosi.ProvisionerRevokeBucketAccessResponse{}, nil
}

func fetchUserCredentials(user rgwadmin.User) string {
	return fmt.Sprintf("[default]\naws_access_key %s\naws_secret_key %s", user.Keys[0].AccessKey, user.Keys[0].SecretKey)
}
