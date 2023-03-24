# kubemark

Custom Kubemark, used to simulate Kubelet and perform large-scale Kubernetes pressure testing

# 编译

```
$ KUBE_BUILD_PLATFORMS=linux/amd64 make WHAT=cmd/kubemark
```

# 新加特性

1. 增加了自定义 kubemark 的 cpu、memory 资源大小;
2. 增加了自定义 kubemark 的 gpu 资源大小;

```
$ kubemark --node-cpu=100 \
           --node-memory=400 \
           --node-gpu=20
```