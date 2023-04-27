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

```
kubelet 自身会定期更新状态到 apiserver，通过参数--node-status-update-frequency指定上报频率，默认是 10s 上报一次。

    1. kube-controller-manager 会每隔--node-monitor-period时间去检查 kubelet 的状态，默认是 5s。
    2. 当 node 失联一段时间后，kubernetes kube-controller-manager 判定 node 为 notready 状态，这段时长通过--node-monitor-grace-period参数配置，默认 40s。
    3. 当 node 失联一段时间后，kubernetes kube-controller-manager 判定 node 为 unhealthy 状态，这段时长通过--node-startup-grace-period参数配置，默认 1m0s。
    4. 当 node 失联一段时间后，kubernetes kube-controller-manager 开始删除原 node 上的 pod，这段时长是通过--pod-eviction-timeout参数配置，默认 5m0s。
    
kube-controller-manager 和 kubelet 是异步工作的，这意味着延迟可能包括任何的网络延迟、apiserver 的延迟、etcd 延迟，一个节点上的负载引起的延迟等等。因此，如果--node-status-update-frequency设置为5s，那么实际上 etcd 中的数据变化会需要 6-7s，甚至更长时间。
```