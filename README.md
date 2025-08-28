# PVC Timing Webhook

This is a Kubernetes webhook，monitoring by kubernetes PVC(CRUD) event record the timing in metadata.annotations with webhook and contoller 


Just to my StressTest


# Install Or Remove

```
kubectl apply -f manifests/

mkdir bin
sh webhook.sh

kubectl delete -f manifests/
```