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
	cosispec "sigs.k8s.io/container-object-storage-interface-spec"
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
			"access_key": "EOE7FYCNOBZJ5VFV909G",
			"secret_key": "qmIqpWm8HxCzmynCrD6U6vKWi4hnDBndOnmxXNsV"
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

func Test_provisionerServer_DriverCreateBucket(t *testing.T) {
	type fields struct {
		provisioner    string
		s3Client       *s3cli.S3Agent
		rgwAdminClient *rgwadmin.API
	}
	type args struct {
		ctx context.Context
		req *cosispec.DriverCreateBucketRequest
	}
	s3Client := &s3cli.S3Agent{
		Client: mockS3Client{},
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosispec.DriverCreateBucketResponse
		wantErr bool
	}{
		{"Empty Bucket Name", fields{"CreateBucket Empty Bucket Name", s3Client, nil}, args{context.Background(), &cosispec.DriverCreateBucketRequest{Name: ""}}, nil, true},
		{"Create Bucket success", fields{"CreateBucket Success", s3Client, nil}, args{context.Background(), &cosispec.DriverCreateBucketRequest{Name: "test-bucket"}}, &cosispec.DriverCreateBucketResponse{BucketId: "test-bucket"}, false},
		{"Create Bucket failure", fields{"CreateBucket Failure", s3Client, nil}, args{context.Background(), &cosispec.DriverCreateBucketRequest{Name: "failed-bucket"}}, nil, true},
		{"Bucket already Exists", fields{"CreateBucket Already Exists", s3Client, nil}, args{context.Background(), &cosispec.DriverCreateBucketRequest{Name: "test-bucket-already-exists"}}, nil, true},
		{"Bucket owned same user", fields{"CreateBucket Owned by same user", s3Client, nil}, args{context.Background(), &cosispec.DriverCreateBucketRequest{Name: "test-bucket-owned-by-same-user"}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &provisionerServer{
				provisioner:    tt.fields.provisioner,
				s3Client:       tt.fields.s3Client,
				rgwAdminClient: tt.fields.rgwAdminClient,
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

func Test_provisionerServer_DriverDeleteBucket(t *testing.T) {
	type fields struct {
		provisioner    string
		s3Client       *s3cli.S3Agent
		rgwAdminClient *rgwadmin.API
	}
	type args struct {
		ctx context.Context
		req *cosispec.DriverDeleteBucketRequest
	}
	s3Client := &s3cli.S3Agent{
		Client: mockS3Client{},
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosispec.DriverDeleteBucketResponse
		wantErr bool
	}{
		{"Empty Bucket Name", fields{"DeleteBucket Empty Bucket Name", s3Client, nil}, args{context.Background(), &cosispec.DriverDeleteBucketRequest{BucketId: ""}}, nil, true},
		{"Delete Bucket success", fields{"DeleteBucket Success", s3Client, nil}, args{context.Background(), &cosispec.DriverDeleteBucketRequest{BucketId: "test-bucket"}}, &cosispec.DriverDeleteBucketResponse{}, false},
		{"Delete Bucket failure", fields{"DeleteBucket Failure", s3Client, nil}, args{context.Background(), &cosispec.DriverDeleteBucketRequest{BucketId: "failed-bucket"}}, nil, true},
		{"Bucket does not exist", fields{"DeleteBucket Does not exist", s3Client, nil}, args{context.Background(), &cosispec.DriverDeleteBucketRequest{BucketId: "test-bucket-does-not-exist"}}, nil, true},
		{"Bucket not empty", fields{"DeleteBucket Not Empty", s3Client, nil}, args{context.Background(), &cosispec.DriverDeleteBucketRequest{BucketId: "test-bucket-not-empty"}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &provisionerServer{
				provisioner:    tt.fields.provisioner,
				s3Client:       tt.fields.s3Client,
				rgwAdminClient: tt.fields.rgwAdminClient,
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

func Test_provisionerServer_DriverGrantBucketAccess(t *testing.T) {
	type fields struct {
		provisioner    string
		s3Client       *s3cli.S3Agent
		rgwAdminClient *rgwadmin.API
	}
	type args struct {
		ctx context.Context
		req *cosispec.DriverGrantBucketAccessRequest
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
	u := rgwadmin.User{}
	err = json.Unmarshal([]byte(userCreateJSON), &u)
	if err != nil {
		t.Fatalf("failed to unmarshal user create json: %v", err)
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosispec.DriverGrantBucketAccessResponse
		wantErr bool
	}{
		{"Empty Bucket Name", fields{"GrantBucketAccess Empty Bucket Name", s3Client, rgwAdminClient}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{BucketId: "", Name: "test-user"}}, nil, true},
		{"Empty User Name", fields{"GrantBucketAccess Empty User Name", s3Client, rgwAdminClient}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{BucketId: "test-bucket", Name: ""}}, nil, true},
		{"Grant Bucket Access success", fields{"GrantBucketAccess Success", s3Client, rgwAdminClient}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{BucketId: "test-bucket", Name: "test-user"}}, &cosispec.DriverGrantBucketAccessResponse{AccountId: "test-user", Credentials: fetchUserCredentials(u)}, false},
		{"Grant Bucket Access failure", fields{"GrantBucketAccess Failure", s3Client, rgwAdminClient}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{BucketId: "failed-bucket", Name: "test-user"}}, nil, true},
		{"Bucket does not exist", fields{"GrantBucketAccess Does not exist", s3Client, rgwAdminClient}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{BucketId: "test-bucket-does-not-exist", Name: "test-user"}}, nil, true},
		{"User does not exist", fields{"GrantBucketAccess User Does not exist", s3Client, rgwAdminClient}, args{context.Background(), &cosispec.DriverGrantBucketAccessRequest{BucketId: "test-bucket", Name: "test-user-does-not-exist"}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &provisionerServer{
				provisioner:    tt.fields.provisioner,
				s3Client:       tt.fields.s3Client,
				rgwAdminClient: tt.fields.rgwAdminClient,
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

func Test_provisionerServer_DriverRevokeBucketAccess(t *testing.T) {
	type fields struct {
		provisioner    string
		s3Client       *s3cli.S3Agent
		rgwAdminClient *rgwadmin.API
	}
	type args struct {
		ctx context.Context
		req *cosispec.DriverRevokeBucketAccessRequest
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

	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosispec.DriverRevokeBucketAccessResponse
		wantErr bool
	}{
		{"Empty user name", fields{"RevokeBucketAccess Empty User Name", nil, rgwAdminClient}, args{context.Background(), &cosispec.DriverRevokeBucketAccessRequest{AccountId: ""}}, nil, true},
		{"Revoke Bucket Access success", fields{"RevokeBucketAccess Success", nil, rgwAdminClient}, args{context.Background(), &cosispec.DriverRevokeBucketAccessRequest{AccountId: "test-user"}}, &cosispec.DriverRevokeBucketAccessResponse{}, false},
		{"Revoke Bucket Access failure", fields{"RevokeBucketAccess Failure", nil, rgwAdminClient}, args{context.Background(), &cosispec.DriverRevokeBucketAccessRequest{AccountId: "failed-user"}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &provisionerServer{
				provisioner:    tt.fields.provisioner,
				s3Client:       tt.fields.s3Client,
				rgwAdminClient: tt.fields.rgwAdminClient,
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
