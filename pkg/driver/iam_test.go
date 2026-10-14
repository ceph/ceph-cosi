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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/iam/iamiface"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/container-object-storage-interface/client/apis/objectstorage/v1alpha1"
	fakebucketclientset "sigs.k8s.io/container-object-storage-interface/client/clientset/versioned/fake"
	cosispec "sigs.k8s.io/container-object-storage-interface/proto"
)

type fakeIAMClient struct {
	iamiface.IAMAPI
	users       map[string]*iam.User
	keys        map[string]map[string]string
	policies    map[string]string
	createdKeys int
}

func newFakeIAMClient() *fakeIAMClient {
	return &fakeIAMClient{users: map[string]*iam.User{}, keys: map[string]map[string]string{}, policies: map[string]string{}}
}

func (f *fakeIAMClient) GetUserWithContext(_ aws.Context, input *iam.GetUserInput, _ ...request.Option) (*iam.GetUserOutput, error) {
	user := f.users[aws.StringValue(input.UserName)]
	if user == nil {
		return nil, awserr.New("NoSuchEntity", "user not found", nil)
	}
	return &iam.GetUserOutput{User: user}, nil
}

func (f *fakeIAMClient) CreateUserWithContext(_ aws.Context, input *iam.CreateUserInput, _ ...request.Option) (*iam.CreateUserOutput, error) {
	name := aws.StringValue(input.UserName)
	if f.users[name] != nil {
		return nil, awserr.New("EntityAlreadyExists", "user exists", nil)
	}
	user := &iam.User{UserName: input.UserName, Path: input.Path}
	f.users[name] = user
	f.keys[name] = map[string]string{}
	return &iam.CreateUserOutput{User: user}, nil
}

func (f *fakeIAMClient) PutUserPolicyWithContext(_ aws.Context, input *iam.PutUserPolicyInput, _ ...request.Option) (*iam.PutUserPolicyOutput, error) {
	f.policies[aws.StringValue(input.UserName)] = aws.StringValue(input.PolicyDocument)
	return &iam.PutUserPolicyOutput{}, nil
}

func (f *fakeIAMClient) ListAccessKeysPagesWithContext(_ aws.Context, input *iam.ListAccessKeysInput, fn func(*iam.ListAccessKeysOutput, bool) bool, _ ...request.Option) error {
	var result []*iam.AccessKeyMetadata
	for id := range f.keys[aws.StringValue(input.UserName)] {
		result = append(result, &iam.AccessKeyMetadata{AccessKeyId: aws.String(id)})
	}
	// Return one key per page to exercise collecting all pages during grant and revoke.
	for i, key := range result {
		if !fn(&iam.ListAccessKeysOutput{AccessKeyMetadata: []*iam.AccessKeyMetadata{key}}, i == len(result)-1) {
			break
		}
	}
	return nil
}

func (f *fakeIAMClient) CreateAccessKeyWithContext(_ aws.Context, input *iam.CreateAccessKeyInput, _ ...request.Option) (*iam.CreateAccessKeyOutput, error) {
	name := aws.StringValue(input.UserName)
	f.createdKeys++
	id := fmt.Sprintf("key-%d", f.createdKeys)
	secret := fmt.Sprintf("secret-%d", f.createdKeys)
	f.keys[name][id] = secret
	return &iam.CreateAccessKeyOutput{AccessKey: &iam.AccessKey{AccessKeyId: aws.String(id), SecretAccessKey: aws.String(secret)}}, nil
}

func (f *fakeIAMClient) DeleteAccessKeyWithContext(_ aws.Context, input *iam.DeleteAccessKeyInput, _ ...request.Option) (*iam.DeleteAccessKeyOutput, error) {
	delete(f.keys[aws.StringValue(input.UserName)], aws.StringValue(input.AccessKeyId))
	return &iam.DeleteAccessKeyOutput{}, nil
}

func (f *fakeIAMClient) DeleteUserPolicyWithContext(_ aws.Context, input *iam.DeleteUserPolicyInput, _ ...request.Option) (*iam.DeleteUserPolicyOutput, error) {
	delete(f.policies, aws.StringValue(input.UserName))
	return &iam.DeleteUserPolicyOutput{}, nil
}

func (f *fakeIAMClient) DeleteUserWithContext(_ aws.Context, input *iam.DeleteUserInput, _ ...request.Option) (*iam.DeleteUserOutput, error) {
	name := aws.StringValue(input.UserName)
	if len(f.keys[name]) > 0 {
		return nil, awserr.New("DeleteConflict", "user still has access keys", nil)
	}
	delete(f.users, name)
	delete(f.keys, name)
	return &iam.DeleteUserOutput{}, nil
}

func iamTestParameters() map[string]string {
	return map[string]string{
		"objectStoreUserSecretName":      "account-credentials",
		"objectStoreUserSecretNamespace": "ceph-cosi",
		iamModeParameter:                 "iam",
		iamAccountParameter:              "RGW00000000000000001",
		iamTenantParameter:               "cluster_a",
	}
}

func iamTestServer(t *testing.T, client *fakeIAMClient) *provisionerServer {
	t.Helper()
	parameters := iamTestParameters()
	bucket := &v1alpha1.Bucket{ObjectMeta: metav1.ObjectMeta{Name: "test-bucket"}, Spec: v1alpha1.BucketSpec{Parameters: parameters}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "account-credentials", Namespace: "ceph-cosi"}, Data: map[string][]byte{
		"AccessKey": []byte("root-key"), "SecretKey": []byte("root-secret"), "Endpoint": []byte("http://rgw.example:8000"),
	}}
	old := initializeIAMClient
	t.Cleanup(func() { initializeIAMClient = old })
	initializeIAMClient = func(ctx context.Context, clientset kubernetes.Interface, parameters map[string]string) (iamiface.IAMAPI, string, string, error) {
		_, endpoint, namespace, err := InitializeIAMClient(ctx, clientset, parameters)
		return client, endpoint, namespace, err
	}
	return &provisionerServer{Clientset: fake.NewSimpleClientset(secret), BucketClientset: fakebucketclientset.NewSimpleClientset(bucket)}
}

func TestIAMGrantRetryAndRevoke(t *testing.T) {
	client := newFakeIAMClient()
	server := iamTestServer(t, client)
	request := &cosispec.DriverGrantBucketAccessRequest{Name: "access-1", BucketId: "test-bucket", Parameters: iamTestParameters(), AuthenticationType: cosispec.AuthenticationType_Key}
	first, err := server.DriverGrantBucketAccess(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := client.CreateAccessKeyWithContext(context.Background(), &iam.CreateAccessKeyInput{UserName: aws.String(first.GetAccountId())})
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.DriverGrantBucketAccess(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if client.createdKeys != 2 || first.GetAccountId() != second.GetAccountId() ||
		first.GetCredentials()["s3"].GetSecrets()["accessSecretKey"] != second.GetCredentials()["s3"].GetSecrets()["accessSecretKey"] {
		t.Fatal("grant retry did not reuse its stored IAM key")
	}
	if _, ok := client.keys[first.GetAccountId()][aws.StringValue(extra.AccessKey.AccessKeyId)]; !ok {
		t.Fatal("grant retry removed an additional IAM key despite having valid stored credentials")
	}
	var policy struct {
		Statement []struct {
			Resource string `json:"Resource"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(client.policies[first.GetAccountId()]), &policy); err != nil {
		t.Fatal(err)
	}
	if len(policy.Statement) != 2 || policy.Statement[0].Resource != "arn:aws:s3::cluster_a:test-bucket" || policy.Statement[1].Resource != "arn:aws:s3::cluster_a:test-bucket/*" {
		t.Fatalf("unexpected IAM policy: %+v", policy)
	}
	revoke := &cosispec.DriverRevokeBucketAccessRequest{BucketId: "test-bucket", AccountId: first.GetAccountId()}
	for i := 0; i < 2; i++ {
		if _, err := server.DriverRevokeBucketAccess(context.Background(), revoke); err != nil {
			t.Fatal(err)
		}
	}
	if len(client.users) != 0 || len(client.policies) != 0 {
		t.Fatal("revoke left IAM resources behind")
	}
	_, cacheName := iamIdentity(iamTestParameters()[iamAccountParameter], request.GetName())
	stored, err := server.loadIAMCredential(context.Background(), "ceph-cosi", cacheName)
	if err != nil || stored != nil {
		t.Fatal(err)
	}
}

func TestIAMGrantReplacesOrphanKey(t *testing.T) {
	client := newFakeIAMClient()
	server := iamTestServer(t, client)
	user, _ := iamIdentity(iamTestParameters()[iamAccountParameter], "access-1")
	if _, err := client.CreateUserWithContext(context.Background(), &iam.CreateUserInput{UserName: aws.String(user), Path: aws.String(iamUserPath("test-bucket"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateAccessKeyWithContext(context.Background(), &iam.CreateAccessKeyInput{UserName: aws.String(user)}); err != nil {
		t.Fatal(err)
	}
	response, err := server.DriverGrantBucketAccess(context.Background(), &cosispec.DriverGrantBucketAccessRequest{Name: "access-1", BucketId: "test-bucket", Parameters: iamTestParameters(), AuthenticationType: cosispec.AuthenticationType_Key})
	if err != nil {
		t.Fatal(err)
	}
	if client.createdKeys != 2 || len(client.keys[user]) != 1 || response.GetCredentials()["s3"].GetSecrets()["accessKeyID"] != "key-2" {
		t.Fatal("orphan IAM key was not replaced")
	}
}

func TestIAMGrantRejectsOtherAccount(t *testing.T) {
	client := newFakeIAMClient()
	server := iamTestServer(t, client)
	parameters := iamTestParameters()
	parameters[iamAccountParameter] = "RGW00000000000000002"
	_, err := server.DriverGrantBucketAccess(context.Background(), &cosispec.DriverGrantBucketAccessRequest{Name: "access-1", BucketId: "test-bucket", Parameters: parameters, AuthenticationType: cosispec.AuthenticationType_Key})
	if status.Code(err) != codes.InvalidArgument || len(client.users) != 0 {
		t.Fatalf("mismatched account returned %v", err)
	}
}

func TestIAMBucketPolicyTenant(t *testing.T) {
	for _, tt := range []struct {
		tenant, bucketResource, objectResource string
	}{
		{"", "arn:aws:s3:::test-bucket", "arn:aws:s3:::test-bucket/*"},
		{"cluster_a", "arn:aws:s3::cluster_a:test-bucket", "arn:aws:s3::cluster_a:test-bucket/*"},
	} {
		t.Run(tt.tenant, func(t *testing.T) {
			policy, err := iamBucketPolicy("test-bucket", tt.tenant)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Statement []struct {
					Resource string `json:"Resource"`
				} `json:"Statement"`
			}
			if err := json.Unmarshal([]byte(policy), &doc); err != nil {
				t.Fatal(err)
			}
			if len(doc.Statement) != 2 || doc.Statement[0].Resource != tt.bucketResource || doc.Statement[1].Resource != tt.objectResource {
				t.Fatalf("unexpected tenant resources: %+v", doc)
			}
		})
	}
}
