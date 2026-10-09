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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"

	s3cli "github.com/ceph/cosi-driver-ceph/pkg/util/s3client"

	rgwadmin "github.com/ceph/go-ceph/rgw/admin"
	"k8s.io/client-go/kubernetes"
	cosispec "sigs.k8s.io/container-object-storage-interface/proto"
)

const (
	userCreateJSON = `{
	"user_id": "test-user",
	"display_name": "test-user",
	"email": "",
	"suspended": 0,
	"max_buckets": 1000,
	"subusers": [],
	"keys": [
		{
			"user": "test-user",
			"access_key": "AccessKey",
			"secret_key": "SecretKey"
		}
	],
	"swift_keys": [],
	"caps": [
		{
			"type": "users",
			"perm": "*"
		}
	],
	"op_mask": "read, write, delete",
	"default_placement": "",
	"default_storage_class": "",
	"placement_tags": [],
	"bucket_quota": {
		"enabled": false,
		"check_on_raw": false,
		"max_size": -1,
		"max_size_kb": 0,
		"max_objects": -1
	},
	"user_quota": {
		"enabled": false,
		"check_on_raw": false,
		"max_size": -1,
		"max_size_kb": 0,
		"max_objects": -1
	},
	"temp_url_keys": [],
	"type": "rgw",
	"mfa_ids": []
}`
)

func createParameters() map[string]string {
	return map[string]string{
		"objectStoreUserSecretName":      "test-user-secret",
		"objectStoreUserSecretNamespace": "test-namespace",
	}
}

func Test_provisionerServer_DriverGenerateBucketId(t *testing.T) {
	type fields struct {
		provisioner string
	}
	type args struct {
		ctx context.Context
		req *cosispec.DriverGenerateBucketIdRequest
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosispec.DriverGenerateBucketIdResponse
		wantErr bool
	}{
		{"Empty Name", fields{"GenerateBucketId Empty Name"}, args{context.Background(), &cosispec.DriverGenerateBucketIdRequest{Name: ""}}, nil, true},
		{"Valid Name", fields{"GenerateBucketId Valid Name"}, args{context.Background(), &cosispec.DriverGenerateBucketIdRequest{Name: "my-bucket"}}, &cosispec.DriverGenerateBucketIdResponse{BucketId: "my-bucket"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &provisionerServer{
				Provisioner: tt.fields.provisioner,
			}
			got, err := s.DriverGenerateBucketId(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("provisionerServer.DriverGenerateBucketId() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("provisionerServer.DriverGenerateBucketId() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_provisionerServer_DriverCreateBucket(t *testing.T) {
	type fields struct {
		provisioner string
	}

	type args struct {
		ctx context.Context
		req *cosispec.DriverCreateBucketRequest
	}

	initializeClients = func(ctx context.Context, clientset kubernetes.Interface, parameters map[string]string) (*s3cli.S3Agent, *rgwadmin.API, error) {
		_, _, err := fetchSecretNameAndNamespace(parameters)
		if err != nil {
			t.Fatalf("failed to fetch secret name and namespace: %v", err)
		}
		s3Client := &s3cli.S3Agent{
			Client: mockS3Client{},
		}
		rgwAdminClient, err := rgwadmin.New("rgw-my-store:8000", "accesskey", "secretkey", nil)
		if err != nil {
			t.Fatalf("failed to create rgw admin client: %v", err)
		}
		return s3Client, rgwAdminClient, nil
	}

	expectedSuccessResp := &cosispec.DriverCreateBucketResponse{
		Protocols: &cosispec.ObjectProtocolAndBucketInfo{
			S3: &cosispec.S3BucketInfo{
				BucketId: "test-bucket",
				Endpoint: "rgw-my-store:8000",
				AddressingStyle: &cosispec.S3AddressingStyle{
					Style: cosispec.S3AddressingStyle_PATH,
				},
			},
		},
	}

	expectedOwnedByYouResp := &cosispec.DriverCreateBucketResponse{
		Protocols: &cosispec.ObjectProtocolAndBucketInfo{
			S3: &cosispec.S3BucketInfo{
				BucketId: "test-bucket-owned-by-you",
				Endpoint: "rgw-my-store:8000",
				AddressingStyle: &cosispec.S3AddressingStyle{
					Style: cosispec.S3AddressingStyle_PATH,
				},
			},
		},
	}

	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosispec.DriverCreateBucketResponse
		wantErr bool
	}{
		{"Empty Bucket ID", fields{"CreateBucket Empty Bucket ID"}, args{context.Background(), &cosispec.DriverCreateBucketRequest{BucketId: "", Parameters: createParameters()}}, nil, true},
		{"Create Bucket success", fields{"CreateBucket Success"}, args{context.Background(), &cosispec.DriverCreateBucketRequest{BucketId: "test-bucket", Parameters: createParameters()}}, expectedSuccessResp, false},
		{"Create Bucket failure", fields{"CreateBucket Failure"}, args{context.Background(), &cosispec.DriverCreateBucketRequest{BucketId: "failed-bucket", Parameters: createParameters()}}, nil, true},
		{"Bucket already Exists", fields{"CreateBucket Already Exists"}, args{context.Background(), &cosispec.DriverCreateBucketRequest{BucketId: "test-bucket-already-exists", Parameters: createParameters()}}, nil, true},
		{"Bucket owned same user", fields{"CreateBucket Owned by same user"}, args{context.Background(), &cosispec.DriverCreateBucketRequest{BucketId: "test-bucket-owned-by-you", Parameters: createParameters()}}, expectedOwnedByYouResp, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &provisionerServer{
				Provisioner: tt.fields.provisioner,
			}
			got, err := s.DriverCreateBucket(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("provisionerServer.DriverCreateBucket() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("provisionerServer.DriverCreateBucket() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_provisionerServer_DriverGetBucket(t *testing.T) {
	type fields struct {
		provisioner string
	}

	type args struct {
		ctx context.Context
		req *cosispec.DriverGetBucketRequest
	}

	initializeClients = func(ctx context.Context, clientset kubernetes.Interface, parameters map[string]string) (*s3cli.S3Agent, *rgwadmin.API, error) {
		_, _, err := fetchSecretNameAndNamespace(parameters)
		if err != nil {
			t.Fatalf("failed to fetch secret name and namespace: %v", err)
		}
		s3Client := &s3cli.S3Agent{
			Client: mockS3Client{},
		}
		rgwAdminClient, err := rgwadmin.New("rgw-my-store:8000", "accesskey", "secretkey", nil)
		if err != nil {
			t.Fatalf("failed to create rgw admin client: %v", err)
		}
		return s3Client, rgwAdminClient, nil
	}

	expectedSuccessResp := &cosispec.DriverGetBucketResponse{
		Protocols: &cosispec.ObjectProtocolAndBucketInfo{
			S3: &cosispec.S3BucketInfo{
				BucketId: "test-bucket",
				Endpoint: "rgw-my-store:8000",
				AddressingStyle: &cosispec.S3AddressingStyle{
					Style: cosispec.S3AddressingStyle_PATH,
				},
			},
		},
	}

	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosispec.DriverGetBucketResponse
		wantErr bool
	}{
		{"Empty Bucket ID", fields{"GetBucket Empty Bucket ID"}, args{context.Background(), &cosispec.DriverGetBucketRequest{BucketId: "", Parameters: createParameters()}}, nil, true},
		{"Get Bucket success", fields{"GetBucket Success"}, args{context.Background(), &cosispec.DriverGetBucketRequest{BucketId: "test-bucket", Parameters: createParameters()}}, expectedSuccessResp, false},
		{"Bucket does not exist", fields{"GetBucket Does not exist"}, args{context.Background(), &cosispec.DriverGetBucketRequest{BucketId: "test-bucket-does-not-exist", Parameters: createParameters()}}, nil, true},
		{"Get Bucket failure", fields{"GetBucket Failure"}, args{context.Background(), &cosispec.DriverGetBucketRequest{BucketId: "test-bucket-fail-internal", Parameters: createParameters()}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &provisionerServer{
				Provisioner: tt.fields.provisioner,
			}
			got, err := s.DriverGetBucket(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("provisionerServer.DriverGetBucket() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("provisionerServer.DriverGetBucket() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_provisionerServer_DriverGrantBucketAccess(t *testing.T) {
	type fields struct {
		provisioner string
	}
	type args struct {
		ctx context.Context
		req *cosispec.DriverGrantBucketAccessRequest
	}
	initializeClients = func(ctx context.Context, clientset kubernetes.Interface, parameters map[string]string) (*s3cli.S3Agent, *rgwadmin.API, error) {
		_, _, err := fetchSecretNameAndNamespace(parameters)
		if err != nil {
			t.Fatalf("failed to fetch secret name and namespace: %v", err)
		}

		s3Client := &s3cli.S3Agent{
			Client: mockS3Client{},
		}
		mockClient := &MockClient{
			MockDo: func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPut {
					if req.URL.RawQuery == "display-name=test-user&format=json&uid=test-user" {
						return &http.Response{
							StatusCode: 200,
							Body:       io.NopCloser(bytes.NewReader([]byte(userCreateJSON))),
						}, nil
					}
				}
				return nil, fmt.Errorf("unexpected request: %q. method %q. path %q", req.URL.RawQuery, req.Method, req.URL.Path)
			},
		}
		rgwAdminClient, err := rgwadmin.New("rgw-my-store:8000", "accesskey", "secretkey", mockClient)
		if err != nil {
			t.Fatalf("failed to create rgw admin client: %v", err)
		}
		return s3Client, rgwAdminClient, nil
	}
	u := rgwadmin.User{}
	err := json.Unmarshal([]byte(userCreateJSON), &u)
	if err != nil {
		t.Fatalf("failed to unmarshal user create json: %v", err)
	}

	expectedGrantResp := &cosispec.DriverGrantBucketAccessResponse{
		AccountId: "test-user",
		Buckets: []*cosispec.DriverGrantBucketAccessResponse_BucketInfo{
			{
				BucketId: "test-bucket",
				BucketInfo: &cosispec.ObjectProtocolAndBucketInfo{
					S3: &cosispec.S3BucketInfo{
						BucketId: "test-bucket",
						Endpoint: "rgw-my-store:8000",
						AddressingStyle: &cosispec.S3AddressingStyle{
							Style: cosispec.S3AddressingStyle_PATH,
						},
					},
				},
			},
		},
		Credentials: fetchUserCredentials(u),
	}

	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosispec.DriverGrantBucketAccessResponse
		wantErr bool
	}{
		{"Empty Bucket List", fields{"GrantBucketAccess Empty Bucket List"}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{AccountName: "test-user", Parameters: createParameters()}}, nil, true},
		{"Empty Bucket Name", fields{"GrantBucketAccess Empty Bucket Name"}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{Buckets: []*cosispec.DriverGrantBucketAccessRequest_AccessedBucket{{BucketId: ""}}, AccountName: "test-user", Parameters: createParameters()}}, nil, true},
		{"Empty User Name", fields{"GrantBucketAccess Empty User Name"}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{Buckets: []*cosispec.DriverGrantBucketAccessRequest_AccessedBucket{{BucketId: "test-bucket"}}, AccountName: "", Parameters: createParameters()}}, nil, true},
		{"Grant Bucket Access success", fields{"GrantBucketAccess Success"}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{Buckets: []*cosispec.DriverGrantBucketAccessRequest_AccessedBucket{{BucketId: "test-bucket"}}, AccountName: "test-user", Parameters: createParameters()}}, expectedGrantResp, false},
		{"Grant Bucket Access failure", fields{"GrantBucketAccess Failure"}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{Buckets: []*cosispec.DriverGrantBucketAccessRequest_AccessedBucket{{BucketId: "failed-bucket"}}, AccountName: "test-user", Parameters: createParameters()}}, nil, true},
		{"Bucket does not exist", fields{"GrantBucketAccess Does not exist"}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{Buckets: []*cosispec.DriverGrantBucketAccessRequest_AccessedBucket{{BucketId: "test-bucket-does-not-exist"}}, AccountName: "test-user", Parameters: createParameters()}}, nil, true},
		{"User does not exist", fields{"GrantBucketAccess User Does not exist"}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{Buckets: []*cosispec.DriverGrantBucketAccessRequest_AccessedBucket{{BucketId: "test-bucket"}}, AccountName: "test-user-does-not-exist", Parameters: createParameters()}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &provisionerServer{
				Provisioner: tt.fields.provisioner,
			}
			got, err := s.DriverGrantBucketAccess(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("provisionerServer.DriverGrantBucketAccess() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("provisionerServer.DriverGrantBucketAccess() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_provisionerServer_DriverDeleteBucket(t *testing.T) {
	type fields struct {
		provisioner string
	}

	type args struct {
		ctx context.Context
		req *cosispec.DriverDeleteBucketRequest
	}

	initializeClients = func(ctx context.Context, clientset kubernetes.Interface, parameters map[string]string) (*s3cli.S3Agent, *rgwadmin.API, error) {
		_, _, err := fetchSecretNameAndNamespace(parameters)
		if err != nil {
			t.Fatalf("failed to fetch secret name and namespace: %v", err)
		}
		s3Client := &s3cli.S3Agent{
			Client: mockS3Client{},
		}
		return s3Client, nil, nil
	}

	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosispec.DriverDeleteBucketResponse
		wantErr bool
	}{
		{"Empty Bucket Name", fields{"DeleteBucket Empty Bucket Name"}, args{context.Background(), &cosispec.DriverDeleteBucketRequest{BucketId: "", Parameters: createParameters()}}, nil, true},
		{"Delete Bucket success", fields{"DeleteBucket Success"}, args{context.Background(), &cosispec.DriverDeleteBucketRequest{BucketId: "test-bucket", Parameters: createParameters()}}, &cosispec.DriverDeleteBucketResponse{}, false},
		{"Delete Bucket failure", fields{"DeleteBucket Failure"}, args{context.Background(), &cosispec.DriverDeleteBucketRequest{BucketId: "test-bucket-fail-internal", Parameters: createParameters()}}, nil, true},
		{"Bucket does not exist (already deleted)", fields{"DeleteBucket Does not exist"}, args{context.Background(), &cosispec.DriverDeleteBucketRequest{BucketId: "test-bucket-does-not-exist", Parameters: createParameters()}}, &cosispec.DriverDeleteBucketResponse{}, false},
		{"Bucket not empty", fields{"DeleteBucket Not Empty"}, args{context.Background(), &cosispec.DriverDeleteBucketRequest{BucketId: "test-bucket-not-empty", Parameters: createParameters()}}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &provisionerServer{
				Provisioner: tt.fields.provisioner,
			}
			got, err := s.DriverDeleteBucket(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("provisionerServer.DriverDeleteBucket() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("provisionerServer.DriverDeleteBucket() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_provisonerServer_DriverRevokeBucketAccess(t *testing.T) {
	type fields struct {
		provisioner string
	}
	type args struct {
		ctx context.Context
		req *cosispec.DriverRevokeBucketAccessRequest
	}

	initializeClients = func(ctx context.Context, clientset kubernetes.Interface, parameters map[string]string) (*s3cli.S3Agent, *rgwadmin.API, error) {
		_, _, err := fetchSecretNameAndNamespace(parameters)
		if err != nil {
			t.Fatalf("failed to fetch secret name and namespace: %v", err)
		}
		s3Client := &s3cli.S3Agent{
			Client: mockS3Client{},
		}
		mockClient := &MockClient{
			MockDo: func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					if req.URL.RawQuery == "format=json&uid=test-user" {
						return &http.Response{
							StatusCode: 200,
							Body:       io.NopCloser(bytes.NewReader([]byte(`[]`))),
						}, nil
					}
				}
				return nil, fmt.Errorf("unexpected request: %q. method %q. path %q", req.URL.RawQuery, req.Method, req.URL.Path)
			},
		}

		rgwAdminClient, err := rgwadmin.New("rgw-my-store:8000", "accesskey", "secretkey", mockClient)
		if err != nil {
			t.Fatalf("failed to create rgw admin client: %v", err)
		}
		return s3Client, rgwAdminClient, nil
	}

	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosispec.DriverRevokeBucketAccessResponse
		wantErr bool
	}{
		{"Empty User Name", fields{"RevokeBucketAccess Empty User Name"}, args{context.Background(), &cosispec.DriverRevokeBucketAccessRequest{AccountId: "", Parameters: createParameters()}}, nil, true},
		{"Revoke Bucket Access success", fields{"RevokeBucketAccess Success"}, args{context.Background(), &cosispec.DriverRevokeBucketAccessRequest{AccountId: "test-user", Parameters: createParameters()}}, &cosispec.DriverRevokeBucketAccessResponse{}, false},
		{"Revoke Bucket Access failure", fields{"RevokeBucketAccess Failure"}, args{context.Background(), &cosispec.DriverRevokeBucketAccessRequest{AccountId: "failed-user", Parameters: createParameters()}}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &provisionerServer{
				Provisioner: tt.fields.provisioner,
			}
			got, err := s.DriverRevokeBucketAccess(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("provisionerServer.DriverRevokeBucketAccess() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("provisionerServer.DriverRevokeBucketAccess() = %v, want %v", got, tt.want)
			}
		})
	}
}
