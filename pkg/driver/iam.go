/*
Copyright 2026 The Ceph-COSI Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	cosispec "sigs.k8s.io/container-object-storage-interface/proto"
)

const (
	iamModeParameter    = "userProvisioningMode"
	iamAccountParameter = "rgwAccountID"
	iamTenantParameter  = "rgwTenant"
	iamPolicyName       = "COSI-BucketAccess"
)

var initializeIAMClient = InitializeIAMClient

func validateProvisioningMode(parameters map[string]string) error {
	switch parameters[iamModeParameter] {
	case "", "adminops":
		return nil
	case "iam":
		if parameters[iamAccountParameter] == "" {
			return status.Error(codes.InvalidArgument, "rgwAccountID is required in IAM mode")
		}
		return nil
	default:
		return status.Error(codes.InvalidArgument, "unsupported userProvisioningMode")
	}
}

// InitializeIAMClient returns the IAM client, RGW endpoint, and credential Secret namespace.
func InitializeIAMClient(ctx context.Context, clientset kubernetes.Interface, parameters map[string]string) (iamiface.IAMAPI, string, string, error) {
	name, namespace, err := fetchSecretNameAndNamespace(parameters)
	if err != nil {
		return nil, "", "", err
	}
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, "", "", fmt.Errorf("get RGW credentials: %w", err)
	}
	accessKey, secretKey, endpoint, _, err := fetchParameters(secret.Data)
	if err != nil {
		return nil, "", "", err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	config := aws.NewConfig().
		WithRegion("us-east-1").
		WithEndpoint(endpoint).
		WithCredentials(credentials.NewStaticCredentials(accessKey, secretKey, "")).
		WithHTTPClient(client)
	sess, err := session.NewSession(config)
	if err != nil {
		return nil, "", "", fmt.Errorf("create IAM session: %w", err)
	}
	return iam.New(sess), endpoint, namespace, nil
}

func iamIdentity(accountID, requestName string) (string, string) {
	digest := sha256.Sum256([]byte(accountID + ":" + requestName))
	suffix := hex.EncodeToString(digest[:20])
	return "cosi-" + suffix, "ceph-cosi-iam-" + suffix
}

func iamUserPath(bucketName string) string {
	digest := sha256.Sum256([]byte(bucketName))
	return "/ceph-cosi/" + hex.EncodeToString(digest[:8]) + "/"
}

func isIAMNotFound(err error) bool {
	var apiErr awserr.Error
	return err != nil && errors.As(err, &apiErr) && apiErr.Code() == "NoSuchEntity"
}

type iamPolicyStatement struct {
	Effect   string   `json:"Effect"`
	Action   []string `json:"Action"`
	Resource string   `json:"Resource"`
}

func iamBucketPolicy(bucketName, tenant string) (string, error) {
	bucket := "arn:aws:s3::" + tenant + ":" + bucketName
	policy := struct {
		Version   string               `json:"Version"`
		Statement []iamPolicyStatement `json:"Statement"`
	}{
		Version: "2012-10-17",
		Statement: []iamPolicyStatement{
			{Effect: "Allow", Action: []string{"s3:ListBucket", "s3:ListBucketVersions", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"}, Resource: bucket},
			{Effect: "Allow", Action: []string{"s3:GetObject", "s3:GetObjectVersion", "s3:PutObject", "s3:DeleteObject", "s3:DeleteObjectVersion", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"}, Resource: bucket + "/*"},
		},
	}
	data, err := json.Marshal(policy)
	return string(data), err
}

func (s *provisionerServer) getIAMBucket(ctx context.Context, bucketName string) (map[string]string, error) {
	bucket, err := s.BucketClientset.ObjectstorageV1alpha1().Buckets().Get(ctx, bucketName, metav1.GetOptions{})
	if err != nil {
		klog.ErrorS(err, "failed to get bucket for IAM access", "bucketName", bucketName)
		return nil, status.Error(codes.Internal, "failed to get bucket")
	}
	return bucket.Spec.Parameters, nil
}

func (s *provisionerServer) grantIAMBucketAccess(ctx context.Context, req *cosispec.DriverGrantBucketAccessRequest) (*cosispec.DriverGrantBucketAccessResponse, error) {
	if req.GetName() == "" || req.GetBucketId() == "" {
		return nil, status.Error(codes.InvalidArgument, "bucket ID and access name are required")
	}
	if req.GetAuthenticationType() != cosispec.AuthenticationType_Key {
		return nil, status.Error(codes.InvalidArgument, "IAM user provisioning requires COSI Key authentication")
	}
	parameters := req.GetParameters()
	bucketParameters, err := s.getIAMBucket(ctx, req.GetBucketId())
	if err != nil {
		return nil, err
	}
	if bucketParameters[iamModeParameter] != "iam" ||
		bucketParameters[iamAccountParameter] != parameters[iamAccountParameter] ||
		bucketParameters[iamTenantParameter] != parameters[iamTenantParameter] ||
		bucketParameters["objectStoreUserSecretName"] != parameters["objectStoreUserSecretName"] ||
		bucketParameters["objectStoreUserSecretNamespace"] != parameters["objectStoreUserSecretNamespace"] {
		return nil, status.Error(codes.InvalidArgument, "bucket and access class must use the same IAM account and credentials")
	}
	client, endpoint, namespace, err := initializeIAMClient(ctx, s.Clientset, parameters)
	if err != nil {
		klog.ErrorS(err, "failed to initialize IAM client", "bucketName", req.GetBucketId(), "accessName", req.GetName(), "secretName", parameters["objectStoreUserSecretName"], "secretNamespace", parameters["objectStoreUserSecretNamespace"])
		return nil, status.Error(codes.Internal, "failed to initialize IAM client")
	}
	userName, cacheName := iamIdentity(parameters[iamAccountParameter], req.GetName())
	if err := ensureIAMUser(ctx, client, userName, iamUserPath(req.GetBucketId())); err != nil {
		klog.ErrorS(err, "failed to ensure IAM user", "userName", userName, "bucketName", req.GetBucketId())
		return nil, status.Error(codes.Internal, "failed to ensure IAM user")
	}
	policy, err := iamBucketPolicy(req.GetBucketId(), parameters[iamTenantParameter])
	if err != nil {
		klog.ErrorS(err, "failed to create IAM policy", "userName", userName, "bucketName", req.GetBucketId())
		return nil, status.Error(codes.Internal, "failed to create IAM policy")
	}
	_, err = client.PutUserPolicyWithContext(ctx, &iam.PutUserPolicyInput{UserName: aws.String(userName), PolicyName: aws.String(iamPolicyName), PolicyDocument: aws.String(policy)})
	if err != nil {
		klog.ErrorS(err, "failed to set IAM user policy", "userName", userName, "bucketName", req.GetBucketId())
		return nil, status.Error(codes.Internal, "failed to set IAM user policy")
	}
	stored, err := s.loadIAMCredential(ctx, namespace, cacheName)
	if err != nil {
		klog.ErrorS(err, "failed to read stored IAM credentials", "userName", userName, "bucketName", req.GetBucketId(), "secret", klog.KRef(namespace, cacheName))
		return nil, status.Error(codes.Internal, "failed to read stored IAM credentials")
	}
	if stored != nil && !matchingIAMCredential(stored, req.GetBucketId(), userName, parameters) {
		return nil, status.Error(codes.AlreadyExists, "stored IAM credentials belong to a different access")
	}
	keys, err := listIAMAccessKeys(ctx, client, userName)
	if err != nil {
		klog.ErrorS(err, "failed to list IAM access keys", "userName", userName, "bucketName", req.GetBucketId())
		return nil, status.Error(codes.Internal, "failed to list IAM access keys")
	}
	if stored != nil {
		found := false
		for _, key := range keys {
			if key == string(stored.Data["accessKeyID"]) {
				found = true
				break
			}
		}
		if found {
			return iamGrantResponse(userName, endpoint, stored.Data), nil
		}
		if err := s.deleteIAMCredential(ctx, namespace, cacheName); err != nil {
			klog.ErrorS(err, "failed to remove stale IAM credentials", "userName", userName, "bucketName", req.GetBucketId(), "secret", klog.KRef(namespace, cacheName))
			return nil, status.Error(codes.Internal, "failed to remove stale IAM credentials")
		}
	}
	// An interrupted CreateAccessKey may leave a key whose secret cannot be read again.
	// This user is exclusively owned by the driver, so replace any such keys.
	for _, key := range keys {
		if _, err := client.DeleteAccessKeyWithContext(ctx, &iam.DeleteAccessKeyInput{UserName: aws.String(userName), AccessKeyId: aws.String(key)}); err != nil && !isIAMNotFound(err) {
			klog.ErrorS(err, "failed to remove orphan IAM access key", "userName", userName, "bucketName", req.GetBucketId())
			return nil, status.Error(codes.Internal, "failed to remove orphan IAM access key")
		}
	}
	created, err := client.CreateAccessKeyWithContext(ctx, &iam.CreateAccessKeyInput{UserName: aws.String(userName)})
	if err != nil || created == nil || created.AccessKey == nil || aws.StringValue(created.AccessKey.AccessKeyId) == "" || aws.StringValue(created.AccessKey.SecretAccessKey) == "" {
		klog.ErrorS(err, "failed to create IAM access key", "userName", userName, "bucketName", req.GetBucketId())
		return nil, status.Error(codes.Internal, "failed to create IAM access key")
	}
	data := map[string][]byte{
		"accessKeyID":     []byte(aws.StringValue(created.AccessKey.AccessKeyId)),
		"accessSecretKey": []byte(aws.StringValue(created.AccessKey.SecretAccessKey)),
		"accountID":       []byte(parameters[iamAccountParameter]),
		"tenant":          []byte(parameters[iamTenantParameter]),
		"bucket":          []byte(req.GetBucketId()),
		"user":            []byte(userName),
	}
	if err := s.saveIAMCredential(ctx, namespace, cacheName, data); err != nil {
		klog.ErrorS(err, "failed to store IAM credentials", "userName", userName, "bucketName", req.GetBucketId(), "secret", klog.KRef(namespace, cacheName))
		_, cleanupErr := client.DeleteAccessKeyWithContext(ctx, &iam.DeleteAccessKeyInput{UserName: aws.String(userName), AccessKeyId: created.AccessKey.AccessKeyId})
		if cleanupErr != nil {
			klog.ErrorS(cleanupErr, "failed to clean up IAM key after credential storage error", "userName", userName, "bucketName", req.GetBucketId())
		}
		return nil, status.Error(codes.Internal, "failed to store IAM credentials")
	}
	return iamGrantResponse(userName, endpoint, data), nil
}

func (s *provisionerServer) revokeIAMBucketAccess(ctx context.Context, req *cosispec.DriverRevokeBucketAccessRequest, parameters map[string]string) (*cosispec.DriverRevokeBucketAccessResponse, error) {
	if req.GetAccountId() == "" || req.GetBucketId() == "" {
		return nil, status.Error(codes.InvalidArgument, "bucket ID and IAM user are required")
	}
	client, _, namespace, err := initializeIAMClient(ctx, s.Clientset, parameters)
	if err != nil {
		klog.ErrorS(err, "failed to initialize IAM client", "userName", req.GetAccountId(), "bucketName", req.GetBucketId(), "secretName", parameters["objectStoreUserSecretName"], "secretNamespace", parameters["objectStoreUserSecretNamespace"])
		return nil, status.Error(codes.Internal, "failed to initialize IAM client")
	}
	userName := req.GetAccountId()
	if !strings.HasPrefix(userName, "cosi-") {
		return nil, status.Error(codes.InvalidArgument, "IAM user is not managed by this driver")
	}
	cacheName := "ceph-cosi-iam-" + strings.TrimPrefix(userName, "cosi-")
	stored, err := s.loadIAMCredential(ctx, namespace, cacheName)
	if err != nil {
		klog.ErrorS(err, "failed to read stored IAM credentials", "userName", userName, "bucketName", req.GetBucketId(), "secret", klog.KRef(namespace, cacheName))
		return nil, status.Error(codes.Internal, "failed to read stored IAM credentials")
	}
	if stored != nil && !matchingIAMCredential(stored, req.GetBucketId(), userName, parameters) {
		return nil, status.Error(codes.AlreadyExists, "stored IAM credentials belong to a different access")
	}
	user, err := client.GetUserWithContext(ctx, &iam.GetUserInput{UserName: aws.String(userName)})
	if err != nil && !isIAMNotFound(err) {
		klog.ErrorS(err, "failed to get IAM user", "userName", userName, "bucketName", req.GetBucketId())
		return nil, status.Error(codes.Internal, "failed to get IAM user")
	}
	if err == nil {
		if user == nil || user.User == nil || aws.StringValue(user.User.Path) != iamUserPath(req.GetBucketId()) {
			return nil, status.Error(codes.FailedPrecondition, "IAM user is not managed by this driver")
		}
		keys, err := listIAMAccessKeys(ctx, client, userName)
		if err != nil {
			klog.ErrorS(err, "failed to list IAM access keys", "userName", userName, "bucketName", req.GetBucketId())
			return nil, status.Error(codes.Internal, "failed to list IAM access keys")
		}
		for _, key := range keys {
			if _, err := client.DeleteAccessKeyWithContext(ctx, &iam.DeleteAccessKeyInput{UserName: aws.String(userName), AccessKeyId: aws.String(key)}); err != nil && !isIAMNotFound(err) {
				klog.ErrorS(err, "failed to delete IAM access key", "userName", userName, "bucketName", req.GetBucketId())
				return nil, status.Error(codes.Internal, "failed to delete IAM access key")
			}
		}
		if _, err := client.DeleteUserPolicyWithContext(ctx, &iam.DeleteUserPolicyInput{UserName: aws.String(userName), PolicyName: aws.String(iamPolicyName)}); err != nil && !isIAMNotFound(err) {
			klog.ErrorS(err, "failed to delete IAM user policy", "userName", userName, "bucketName", req.GetBucketId())
			return nil, status.Error(codes.Internal, "failed to delete IAM user policy")
		}
		if _, err := client.DeleteUserWithContext(ctx, &iam.DeleteUserInput{UserName: aws.String(userName)}); err != nil && !isIAMNotFound(err) {
			klog.ErrorS(err, "failed to delete IAM user", "userName", userName, "bucketName", req.GetBucketId())
			return nil, status.Error(codes.Internal, "failed to delete IAM user")
		}
	}
	if err := s.deleteIAMCredential(ctx, namespace, cacheName); err != nil {
		klog.ErrorS(err, "failed to delete stored IAM credentials", "userName", userName, "bucketName", req.GetBucketId(), "secret", klog.KRef(namespace, cacheName))
		return nil, status.Error(codes.Internal, "failed to delete stored IAM credentials")
	}
	return &cosispec.DriverRevokeBucketAccessResponse{}, nil
}

func ensureIAMUser(ctx context.Context, client iamiface.IAMAPI, userName, expectedPath string) error {
	result, err := client.GetUserWithContext(ctx, &iam.GetUserInput{UserName: aws.String(userName)})
	var user *iam.User
	if err == nil && result != nil {
		user = result.User
	}
	if isIAMNotFound(err) {
		created, createErr := client.CreateUserWithContext(ctx, &iam.CreateUserInput{UserName: aws.String(userName), Path: aws.String(expectedPath)})
		err = createErr
		if err == nil && created != nil {
			user = created.User
		}
	}
	if err != nil {
		return err
	}
	if user == nil || aws.StringValue(user.Path) != expectedPath {
		return fmt.Errorf("existing IAM user %q is not managed by this driver", userName)
	}
	return nil
}

func listIAMAccessKeys(ctx context.Context, client iamiface.IAMAPI, userName string) ([]string, error) {
	var keys []string
	err := client.ListAccessKeysPagesWithContext(ctx, &iam.ListAccessKeysInput{UserName: aws.String(userName)}, func(out *iam.ListAccessKeysOutput, _ bool) bool {
		for _, key := range out.AccessKeyMetadata {
			if key != nil && key.AccessKeyId != nil {
				keys = append(keys, *key.AccessKeyId)
			}
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func iamGrantResponse(userName, endpoint string, data map[string][]byte) *cosispec.DriverGrantBucketAccessResponse {
	return &cosispec.DriverGrantBucketAccessResponse{
		AccountId: userName,
		Credentials: map[string]*cosispec.CredentialDetails{
			"s3": {Secrets: map[string]string{
				"accessKeyID":     string(data["accessKeyID"]),
				"accessSecretKey": string(data["accessSecretKey"]),
				"endpoint":        endpoint,
				"region":          "us-east-1",
			}},
		},
	}
}

func matchingIAMCredential(secret *corev1.Secret, bucket, user string, parameters map[string]string) bool {
	return string(secret.Data["bucket"]) == bucket &&
		string(secret.Data["user"]) == user &&
		string(secret.Data["accountID"]) == parameters[iamAccountParameter] &&
		string(secret.Data["tenant"]) == parameters[iamTenantParameter] &&
		len(secret.Data["accessKeyID"]) > 0 && len(secret.Data["accessSecretKey"]) > 0
}

func (s *provisionerServer) loadIAMCredential(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	secret, err := s.Clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return secret, err
}

func (s *provisionerServer) saveIAMCredential(ctx context.Context, namespace, name string, data map[string][]byte) error {
	_, err := s.Clientset.CoreV1().Secrets(namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "ceph-cosi"}},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
	}, metav1.CreateOptions{})
	return err
}

func (s *provisionerServer) deleteIAMCredential(ctx context.Context, namespace, name string) error {
	err := s.Clientset.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
