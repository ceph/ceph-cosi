// Copyright 2021 The Ceph-COSI Authors.
// Licensed under the Apache License, Version 2.0 (the "License");
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package driver

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go/aws/awserr"
	radosgwapi "github.com/ceph/go-ceph/rgw/admin"
	"github.com/pkg/errors"
	"github.com/thotz/cosi-driver-ceph/pkg/util/s3client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	cosi "sigs.k8s.io/container-object-storage-interface-spec"
)

// contains two clients
// 1.) for RGWAdminOps : mainly for user related operations
// 2.) for S3 operations : mainly for bucket related operations
type ProvisionerServer struct {
	provisioner        string
	S3Client           *s3client.S3Agent
	radosgwAdminClient *radosgwapi.API
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
		klog.ErrorS(errors.New("Invalid Argument"), "Protocol is nil")
		return nil, status.Error(codes.InvalidArgument, "Protocol is nil")
	}
	s3 := protocol.GetS3()
	if s3 == nil {
		klog.ErrorS(errors.New("Invalid Argument"), "S3 protocol is nil")
		return nil, status.Error(codes.InvalidArgument, "S3 Protocol is nil")
	}
	//TODO : validate S3 protocol defined, check points valid rgwendpoint, v4 signature check etc
	bucketName := req.GetName()
	klog.V(3).InfoS("Create Bucket", "name", bucketName)

	err := s.S3Client.CreateBucket(bucketName)
	if err != nil {
		// Check to see if the bucket already exists by above api
		klog.ErrorS(err, "Bucket creation failed")
		return nil, status.Error(codes.Internal, "Bucket creation failed")
	}
	klog.Infof("Successfully created Backend Bucket %q", bucketName)

	return &cosi.ProvisionerCreateBucketResponse{
		BucketId: bucketName,
	}, nil
}

func (s *ProvisionerServer) ProvisionerDeleteBucket(ctx context.Context,
	req *cosi.ProvisionerDeleteBucketRequest) (*cosi.ProvisionerDeleteBucketResponse, error) {
	klog.Infof("Delete bucket %q", req.GetBucketId())
	if _, err := s.S3Client.DeleteBucket(req.GetBucketId()); err != nil {
		klog.Info("failed to delete bucket %q", req.GetBucketId())
		return nil, status.Error(codes.Internal, "Bucket deletion failed")
	}

	return &cosi.ProvisionerDeleteBucketResponse{}, nil
}

func (s *ProvisionerServer) ProvisionerGrantBucketAccess(ctx context.Context,
	req *cosi.ProvisionerGrantBucketAccessRequest) (*cosi.ProvisionerGrantBucketAccessResponse, error) {
	userName := req.GetAccountName()
	bucketName := req.GetBucketId()
	accessPolicy := req.GetAccessPolicy()
	klog.Info("Granting user %q the policy %q to bucket %q", userName, bucketName, accessPolicy)
	user, err := s.radosgwAdminClient.CreateUser(context.Background(), radosgwapi.User{
		ID:          userName,
		DisplayName: userName,
	})
	if err != nil {
		klog.Error("failed to create user", err)
		return nil, status.Error(codes.Internal, "User creation failed")
	}
	// TODO : Handle access policy in request, currently granting all perms to this user
	policy, err := s.S3Client.GetBucketPolicy(bucketName)
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
	out, err := s.S3Client.PutBucketPolicy(bucketName, *policy)
	if err != nil {
		klog.Error("failed to set policy", err)
		return nil, status.Error(codes.Internal, "puting policy failed")
	}
	klog.Infof("set policy %v", out)

	// TODO : limit the bucket count for this user to 0

	// Below response if not final, may change in future
	return &cosi.ProvisionerGrantBucketAccessResponse{
		AccountId:   userName,
		Credentials: fmt.Sprintf("[default]\naws_access_key %s\naws_secret_key %s", user.Keys[0].AccessKey, user.Keys[0].SecretKey),
	}, nil
}

func (s *ProvisionerServer) ProvisionerRevokeBucketAccess(ctx context.Context,
	req *cosi.ProvisionerRevokeBucketAccessRequest) (*cosi.ProvisionerRevokeBucketAccessResponse, error) {

	// TODO : instead of deleting user, revoke its permission and delete only if no more bucket attached to it
	klog.Infof("Delete user %q", req.GetAccountId())
	if err := s.radosgwAdminClient.RemoveUser(context.Background(), radosgwapi.User{
		ID:          req.GetAccountId(),
		DisplayName: req.GetAccountId(),
	}); err != nil {
		klog.Error("falied to Revoke Bucket Access")
		return nil, status.Error(codes.Internal, "falied to Revoke Bucket Access")
	}
	return &cosi.ProvisionerRevokeBucketAccessResponse{}, nil
}
