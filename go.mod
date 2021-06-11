module github.com/ceph/cosi-driver-ceph

go 1.16

require (
	github.com/aws/aws-sdk-go v1.38.24
	github.com/ceph/go-ceph v0.10.0
	github.com/spf13/cobra v1.1.3
	github.com/spf13/pflag v1.0.5
	github.com/spf13/viper v1.7.1
	golang.org/x/net v0.0.0-20210525063256-abc453219eb5 // indirect
	golang.org/x/sys v0.0.0-20210608053332-aa57babbf139 // indirect
	google.golang.org/genproto v0.0.0-20210608205507-b6d2f5bf0d7d // indirect
	google.golang.org/grpc v1.38.0
	k8s.io/apimachinery v0.19.4
	k8s.io/klog/v2 v2.8.0
	sigs.k8s.io/container-object-storage-interface-provisioner-sidecar v0.0.0-20210528161624-b46634c30d14
	sigs.k8s.io/container-object-storage-interface-spec v0.0.0-20210507203703-a97f2e98ac90
)
