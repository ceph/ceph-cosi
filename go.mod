module github.com/ceph/cosi-driver-ceph

go 1.16

require (
	github.com/aws/aws-sdk-go v1.44.67
	github.com/ceph/go-ceph v0.17.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.7.1
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20210903162649-d08c68adba83 // indirect
	google.golang.org/grpc v1.40.0
	k8s.io/apimachinery v0.19.4
	k8s.io/klog/v2 v2.80.1
	sigs.k8s.io/container-object-storage-interface-provisioner-sidecar v0.0.0-20210528161624-b46634c30d14
	sigs.k8s.io/container-object-storage-interface-spec v0.0.0-20210825023039-e703fa91908a
)
