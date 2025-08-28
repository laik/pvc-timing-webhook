# PVC Timing Webhook

这是一个Kubernetes webhook，用于监控PVC（PersistentVolumeClaim）的状态变化并记录时间信息，同时当PVC状态完成时自动添加注解来触发更新。