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
	"reflect"
	"testing"

	cosi "sigs.k8s.io/container-object-storage-interface/proto"
)

func TestIdentityServer_DriverGetInfo(t *testing.T) {
	type fields struct {
		provisioner string
	}
	type args struct {
		ctx context.Context
		req *cosi.DriverGetInfoRequest
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    *cosi.DriverGetInfoResponse
		wantErr bool
	}{
		{
			name: "Test DriverGetInfo",
			fields: fields{
				provisioner: "ceph-cosi-driver",
			},
			args: args{
				ctx: context.Background(),
				req: &cosi.DriverGetInfoRequest{},
			},
			want: &cosi.DriverGetInfoResponse{
				Name: "ceph-cosi-driver",
			},
			wantErr: false,
		},
		{
			name: "Test DriverGetInfo with empty provisioner name",
			fields: fields{
				provisioner: "",
			},
			args: args{
				ctx: context.Background(),
				req: &cosi.DriverGetInfoRequest{},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := &identityServer{
				provisioner: tt.fields.provisioner,
			}
			got, err := id.DriverGetInfo(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("IdentityServer.DriverGetInfo() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("IdentityServer.DriverGetInfo() = %v, want %v", got, tt.want)
			}
		})
	}
}
