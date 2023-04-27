# kubemark

Custom Kubemark, used to simulate Kubelet and perform large-scale Kubernetes pressure testing

# 编译

```
$ KUBE_BUILD_PLATFORMS=linux/amd64 make WHAT=cmd/kubemark
```

# 新加特性

1. 增加了自定义 kubemark 的 cpu、memory 资源大小;
2. 增加了自定义 kubemark 的 gpu 资源大小;
3. 增加kubelet lease心跳上报间隔的参数；
4. 增加kubelet 更新status的检查间隔时间参数；
5. 增加kubelet 最大更新status的时间间隔参数；
6. 增加kubelet 的pod cidr的参数；

```
$ kubemark --node-cpu=100 \
           --node-memory=400 \
           --node-gpu=20 \
           --node-lease-duration-seconds=240 \
           --node-status-update-frequency=30 \
           --node-status-report-frequency=3000 \
           --pod-cidr="192.168.0.1/8"
```