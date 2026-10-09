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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	rgwadmin "github.com/ceph/go-ceph/rgw/admin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/kubernetes"
	cosispec "sigs.k8s.io/container-object-storage-interface/proto"

	s3cli "github.com/ceph/cosi-driver-ceph/pkg/util/s3client"
)

type grantAccessS3Client struct {
	mockS3Client
	policyReads     int
	policyWrites    int
	failFirstPolicy bool
}

func (m *grantAccessS3Client) GetBucketPolicy(*s3.GetBucketPolicyInput) (*s3.GetBucketPolicyOutput, error) {
	m.policyReads++
	return nil, awserr.New("NoSuchBucketPolicy", "no policy yet", nil)
}

func (m *grantAccessS3Client) PutBucketPolicy(*s3.PutBucketPolicyInput) (*s3.PutBucketPolicyOutput, error) {
	m.policyWrites++
	if m.failFirstPolicy && m.policyWrites == 1 {
		return nil, awserr.New("InternalError", "temporary policy failure", nil)
	}
	return &s3.PutBucketPolicyOutput{}, nil
}

func TestDriverGrantBucketAccessUserCredentials(t *testing.T) {
	tests := []struct {
		name         string
		createStatus int
		createBody   string
		getStatus    int
		getBody      string
		retry        bool
		wantCode     codes.Code
		wantLookups  int
		wantPolicies int
	}{
		{name: "retry after policy failure", createStatus: http.StatusOK, createBody: userCreateJSON, getStatus: http.StatusOK, getBody: userCreateJSON, retry: true, wantLookups: 1, wantPolicies: 2},
		{name: "existing user", createStatus: http.StatusConflict, getStatus: http.StatusOK, getBody: userCreateJSON, wantLookups: 1, wantPolicies: 1},
		{name: "lookup failure", createStatus: http.StatusConflict, getStatus: http.StatusNotFound, getBody: `{"Code":"NoSuchUser"}`, wantCode: codes.Internal, wantLookups: 1},
		{name: "existing user without keys", createStatus: http.StatusConflict, getStatus: http.StatusOK, getBody: `{"user_id":"test-user","keys":[]}`, wantCode: codes.Internal, wantLookups: 1},
		{name: "new user without keys", createStatus: http.StatusOK, createBody: `{"user_id":"test-user","keys":[]}`, wantCode: codes.Internal},
		{name: "empty access key", createStatus: http.StatusOK, createBody: `{"keys":[{"access_key":"","secret_key":"SecretKey"}]}`, wantCode: codes.Internal},
		{name: "empty secret key", createStatus: http.StatusOK, createBody: `{"keys":[{"access_key":"AccessKey","secret_key":""}]}`, wantCode: codes.Internal},
		{name: "creation failure", createStatus: http.StatusForbidden, createBody: `{"Code":"AccessDenied"}`, wantCode: codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s3Client := &grantAccessS3Client{failFirstPolicy: tt.retry}
			creates, lookups := 0, 0
			adminClient, err := rgwadmin.New("http://rgw.example:8000", "admin-access", "admin-secret", &MockClient{
				MockDo: func(req *http.Request) (*http.Response, error) {
					if req.URL.Query().Get("uid") != "test-user" {
						t.Fatalf("unexpected user in request: %s", req.URL.Path)
					}
					code, body := tt.createStatus, tt.createBody
					switch req.Method {
					case http.MethodPut:
						creates++
						if code == http.StatusConflict || creates > 1 {
							code, body = http.StatusConflict, `{"Code":"UserAlreadyExists"}`
						}
					case http.MethodGet:
						lookups++
						code, body = tt.getStatus, tt.getBody
					default:
						t.Fatalf("unexpected admin method: %s", req.Method)
					}
					return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body))}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			originalInitializeClients := initializeClients
			t.Cleanup(func() { initializeClients = originalInitializeClients })
			initializeClients = func(context.Context, kubernetes.Interface, map[string]string) (*s3cli.S3Agent, *rgwadmin.API, error) {
				return &s3cli.S3Agent{Client: s3Client}, adminClient, nil
			}
			server := &provisionerServer{}
			request := &cosispec.DriverGrantBucketAccessRequest{
				AccountName: "test-user",
				Buckets: []*cosispec.DriverGrantBucketAccessRequest_AccessedBucket{
					{BucketId: "test-bucket"},
				},
			}
			if tt.retry {
				if _, err := server.DriverGrantBucketAccess(context.Background(), request); status.Code(err) != codes.Internal {
					t.Fatalf("initial policy failure returned %v", err)
				}
			}
			response, err := server.DriverGrantBucketAccess(context.Background(), request)
			if status.Code(err) != tt.wantCode {
				t.Fatalf("grant returned %v, want %s", err, tt.wantCode)
			}
			if lookups != tt.wantLookups || s3Client.policyReads != tt.wantPolicies || s3Client.policyWrites != tt.wantPolicies {
				t.Fatalf("lookups/reads/writes = %d/%d/%d, want %d/%d/%d", lookups, s3Client.policyReads, s3Client.policyWrites, tt.wantLookups, tt.wantPolicies, tt.wantPolicies)
			}
			if err != nil {
				if response != nil {
					t.Fatal("failed grant returned credentials")
				}
				return
			}
			s3Creds := response.GetCredentials().GetS3()
			if response.GetAccountId() != "test-user" || s3Creds.GetAccessKeyId() != "AccessKey" || s3Creds.GetAccessSecretKey() != "SecretKey" {
				t.Fatal("grant did not return the existing user's credentials")
			}
		})
	}
}
