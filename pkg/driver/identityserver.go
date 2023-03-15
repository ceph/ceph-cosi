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
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"

	cosispec "sigs.k8s.io/container-object-storage-interface-spec"
)

type identityServer struct {
	provisioner string
}
var _ cosispec.IdentityServer = &identityServer{}

func NewIdentityServer(provisionerName string) (cosispec.IdentityServer, error) {
	return &identityServer{
		provisioner: provisionerName,
	}, nil
}
func (id *identityServer) DriverGetInfo(ctx context.Context,
	req *cosispec.DriverGetInfoRequest) (*cosispec.DriverGetInfoResponse, error) {

	if id.provisioner == "" {
		klog.ErrorS(fmt.Errorf("provisioner name cannot be empty"), "invalid argument")
		return nil, status.Error(codes.InvalidArgument, "Provisioner name is empty")
	}

	return &cosispec.DriverGetInfoResponse{
		Name: id.provisioner,
	}, nil
}
