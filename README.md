# swansonator
A Kubernetes Operator that manages the mission-critical web application, [swanson](https://github.com/jacobboykin/swanson).

* [Description](https://github.com/jacobboykin/swansonator#Description)
* [Getting Started](https://github.com/jacobboykin/swansonator#getting-started)
* [Project Notes for Reviewers](https://github.com/jacobboykin/swansonator#project-notes-for-reviewers)

## Description
[swanson](https://github.com/jacobboykin/swanson) is a silly little web server I made just for this Operator project. It renders a GIF of Ron Swanson based on the value of the `SWANSON_KIND` environment variable. The swansonator Operator will manage a Deployment and Cluster IP Service for the swanson web app. The Swanson CRD exposes two values for you to configure your swanson instance:
* **Spec.Kind**: The "kind" of Ron GIF you'd like. The app supports three values, `happy`, `sad`, and `chaos`.
* **Spec.Size**: The number of replicas you'd like the swanson Deployment to have.

## Getting Started
You’ll need a Kubernetes cluster to run against. You can use [kind](https://sigs.k8s.io/kind) or [K3D](https://k3d.io/) to get a local cluster for testing, or run against a remote cluster. This documentation will use kind as an example. Make sure your current context is set to the cluster you'd like the operator deployed to. You can create a local cluster with kind by running the following:
```shell
$ kind create cluster --name swansonator
```
### Running the operator
1. Deploy the operator and its CRDs:
```shell
$ make deploy
```
After a minute or two, the operator will be running in your cluster:
```shell
$ kubectl get po -n swansonator-system
NAME                                              READY   STATUS    RESTARTS   AGE
swansonator-controller-manager-6499d94b9b-2twhj   2/2     Running   0          2m20s
```

### Deploying swanson
1. Deploy an instance of swanson using the sample CR:
```shell
$ kubectl apply -f ./config/samples/parks_v1alpha1_swanson.yaml             
```

The operator will create a Deployment and Service for the swanson app with the desired ron "kind" and size:
```shell
$ kubectl get deploy                                                                   
NAME             READY   UP-TO-DATE   AVAILABLE   AGE
swanson-sample   3/3     3            3           3m58s

$ kubectl get svc                                              
NAME             TYPE        CLUSTER-IP     EXTERNAL-IP   PORT(S)    AGE
kubernetes       ClusterIP   10.96.0.1      <none>        443/TCP    16m
swanson-sample   ClusterIP   10.96.51.147   <none>        8080/TCP   4m15s
```

### Test it out
To save time, this project doesn't include any Ingress or LoadBalancer features.
1. Use port-forwarding to test the swanson app:
```shell
$ kubectl port-forward svc/swanson-sample 8080                                 
Forwarding from 127.0.0.1:8080 -> 8080
Forwarding from [::1]:8080 -> 8080
```
2. Visit the app in your browser:

![Screen Shot 2023-03-29 at 11 26 59 PM](https://user-images.githubusercontent.com/9063688/228722933-611e8bd1-604f-48db-9b70-fdf73ed2222d.png)

### Make a change to the desired state
1. Stop the port-forwarding process (i.e. via ctrl-c). The change will trigger new Pods to roll out, and will break the port-forwarding.
2. Make a change to the desired state. For example, changing the type of Ron GIF:
```shell
$ kubectl patch swanson swanson-sample -p '{"spec":{"kind": "happy"}}' --type=merge
```
3. Wait for the new Pods to roll out after the Deployment is updated. 
4. Start port-forwarding again, and visit the app in your browser:

![Screen Shot 2023-03-29 at 10 26 02 PM](https://user-images.githubusercontent.com/9063688/228722990-afa0ae12-279d-4957-93cf-c5ec5fb13798.png)

### Tear it all down
Once you're done testing, you can uninstall the operator from the cluster by running:
```shell
$ make undeploy
```

## Project Notes for Reviewers

TODO
