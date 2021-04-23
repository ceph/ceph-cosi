# cosi-driver-ceph

Sample Driver that provides reference implementation for Container Object Storage Interface (COSI) API for ceph object store aka Rados Gateway(RGW)

#### Installing CRDS, COSI controller, Node adapter
```
$ kubectl create -k github.com/kubernetes-sigs/container-object-storage-interface-api

$ kubectl create -k github.com/kubernetes-sigs/container-object-storage-interface-controller

$ kubectl create -k github.com/kubernetes-sigs/container-object-storage-interface-csi-adapter
```
Following pods will running in the default namespace :
```
NAME                                        READY   STATUS    RESTARTS   AGE
objectstorage-controller-6fc5f89444-4ws72   1/1     Running   0          2d6h
objectstorage-csi-adapter-wsl4l             3/3     Running   0          2d6h
```


#### Building, Installing, Setting Up
Code can be compiled using:
```
$ cd cmd/ceph-cosi-driver; go build
```
Now build docker image and provide tag as `ceph/ceph-cosi-driver:latest`
```
$ docker build ./
Sending build context to Docker daemon  41.95MB
Step 1/5 : FROM gcr.io/distroless/static:latest
 ---> 1d9948f921db
Step 2/5 : LABEL maintainers="Ceph COSI Authors"
 ---> Using cache
 ---> 8659e9813ec5
Step 3/5 : LABEL description="Ceph COSI driver"
 ---> Using cache
 ---> 0c55b21ff64f
Step 4/5 : COPY ./cmd/ceph-cosi-driver/ceph-cosi-driver ceph-cosi-driver
 ---> a21275402998
Step 5/5 : ENTRYPOINT ["/ceph-cosi-driver"]
 ---> Running in 620bfa992683
Removing intermediate container 620bfa992683
 ---> 09575229056e
Successfully built 09575229056e (set this to "IMAGE_ID")

docker tag $IMAGE_ID ceph/ceph-cosi-driver:latest
```
Now start the sidecar and cosi driver with:
```
$ kubectl apply -k .
$ kubectl -n ceph-cosi-driver get pods
NAME                                         READY   STATUS    RESTARTS   AGE
objectstorage-provisioner-6c8df56cc6-lqr26   2/2     Running   0          26h
```

#### Create Bucket Requests, Bucket Access Request and consuming it in App
```
$ kubectl create -f examples/bucketclass.yaml
$ kubectl create -f examples/bucketrequest.yaml
$ kubectl create -f examples/bucketaccessclass.yaml
$ kubectl create -f examples/bucketaccessrequest.yaml
```
In the app, `bucketaccessrequest(bar)` can be cosumed as volume mount:
```yaml
spec:
  containers:
      volumeMounts:
        - name: cosi-secrets
          mountPath: /data/cosi
  volumes:
  - name: cosi-secrets
    csi:
      driver: objectstorage.k8s.io
      volumeAttributes:
        bar-name: sample-bar
        bar-namespace: default
```
An example for awscli pods can be found at `examples/awscliapppod.yaml`

#### TODO
1. Deletion of Bucket Request and Bucket Access Request not supported
2. Handle access policies for Bucket Access Request
3. Adding unittests and CI job for integration tests
4. Add make file for compiling code, building container image etc

#### Community, discussion, contribution, and support

You can reach the maintainers of this project at:

- [Slack](https://kubernetes.slack.com/messages/sig-storage)
- [Mailing List](https://groups.google.com/forum/#!forum/kubernetes-sig-storage)

##### Code of conduct

Participation in the Kubernetes community is governed by the [Kubernetes Code of Conduct](code-of-conduct.md).
