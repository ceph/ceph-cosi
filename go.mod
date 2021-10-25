module github.com/ceph/cosi-driver-ceph

go 1.16

require (
	github.com/aws/aws-sdk-go v1.38.24
	github.com/ceph/go-ceph v0.11.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.7.1
	golang.org/x/net v0.0.0-20210907225631-ff17edfbf26d // indirect
	golang.org/x/sys v0.0.0-20210906170528-6f6e22806c34 // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20210903162649-d08c68adba83 // indirect
	google.golang.org/grpc v1.40.0
	k8s.io/apimachinery v0.19.4
	k8s.io/klog/v2 v2.8.0
	sigs.k8s.io/container-object-storage-interface-provisioner-sidecar v0.0.0-20210528161624-b46634c30d14
	sigs.k8s.io/container-object-storage-interface-spec v0.0.0-20210825023039-e703fa91908a
)
