
# Configuring the NVIDIA Device Plugin to report Different GPU Models with Volcano Capacity Scheduling

Volcano v1.9.0 introduces capacity scheduling capabilities, allowing you to configure different quotas for various GPU types (essential in production environments). For example:

```yaml
apiVersion: scheduling.volcano.sh/v1beta1
kind: Queue
metadata:
  name: queue1
spec:
  reclaimable: true
  deserved: # set the deserved field.
    cpu: 2
    memory: 8Gi
    nvidia.com/t4: 40
    nvidia.com/a100: 20
```

However, the default Nvidia Device Plugin reports GPU resources as `nvidia.com/gpu`, which does not support reporting different GPU models as shown in the example.

This guide walks you through configuring the NVIDIA Device Plugin to report different GPU models and integrating it with Volcano's capacity scheduling.

## Install a Customized Device Plugin

In this section, we will cover how to install a customized Device Plugin using Helm. Instructions for installing `helm` can be found [here](https://helm.sh/docs/intro/install/). For prerequisites to install the Device Plugin, refer to the [documentation](../../README.md#prerequisites) for detailed instructions. 

If you are using the GPU Operator to manage all GPU-related components, there are two options. One option is to disable the GPU Operator's management of the Device Plugin and follow the steps in this section. The other option is to modify the GPU Operator configuration accordingly.

### 1.1 Install a Custom Device Plugin

Begin by setting up the plugin's `helm` repository and updating it as follows:

```shell
helm repo add nvdp https://nvidia.github.io/k8s-device-plugin
helm repo update
```

Then verify if the version v0.16.1 on which the modified release of the plugin is based exists:

```shell
$ helm search repo nvdp --version 0.16.1
NAME                           CHART VERSION  APP VERSION    DESCRIPTION
nvdp/nvidia-device-plugin      0.16.1     0.16.1        A Helm chart for ...
```

Next, prepare a ConfigMap file, assuming it's named `config.yaml`. A typical configuration is as follows:

```yaml
version: v1
flags:
  migStrategy: "none"
  failOnInitError: true
  nvidiaDriverRoot: "/"
  plugin:
    passDeviceSpecs: false
    deviceListStrategy: "envvar"
    deviceIDStrategy: "uuid"
```

Once this repo is updated and the ConfigMap is prepared, you can begin installing packages from it to deploy the `volcano-device-plugin`.

```shell
helm upgrade -i nvdp nvdp/nvidia-device-plugin \
    --version=0.16.1 \
    --namespace nvidia-device-plugin \
    --create-namespace \
    --set gfd.enabled=true \
    --set image.repository=volcanosh/volcano-device-plugin \
    --set image.tag=v1.1.0 \
    --set config.default=default-config \
    --set-file config.map.default-config=config.yaml
```

Here is a brief explanation of some of the configuration parameters:

| Command                                           | Usage                                                                                                                                                                   |
| ------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--set gfd.enabled=true`                          | Enables the GFD feature. If you already have NFD deployed on your cluster and do not wish for it to be pulled in by this installation, you can disable it with `nfd.enabled=false`. |
| `--set image.repository`<br>`--set image.tag`     | Replaces the official image with the modified version.                                                                                                                                       |
| `--set config.default`<br>`--set-file config.map` | Sets the ConfigMap for the Device Plugin to configure resource renaming rules.                                                                                                                                 |

If you wish to set multiple ConfigMaps to allow different policies for different nodes or configure Time Slicing, refer to the [documentation](../../README.md#multiple-config-file-example) for more information.

The _Volcano_ community provides an image based on NVIDIA Device Plugin 0.16.1. If this version does not meet your needs, you can refer to the [discussion](https://github.com/NVIDIA/k8s-device-plugin/issues/424) and create your custom image that supports Resource Naming.

### 1.2 Set Resource Renaming Rules

Once the Device Plugin is deployed, to report resources like `nvidia.com/a100`, you need to update the ConfigMap to provide renaming rules, for example:

```yaml
version: v1
flags:
  migStrategy: "none"
  failOnInitError: true
  nvidiaDriverRoot: "/"
  plugin:
    passDeviceSpecs: false
    deviceListStrategy: envvar
    deviceIDStrategy: uuid
# new
resources:
  gpus:
  - pattern: "A100-*-40GB"
    name: a100-40gb
  - pattern: "*A100*"
    name: a100
```

The `pattern` field can be checked on your node by running the command:

```shell
$ nvidia-smi --query-gpu=name --format=csv,noheader
A100-SXM4-40GB
...
```

Patterns can include wildcards (i.e. ‘*’) to match against multiple devices with similar names. Additionally, the order of the entries under `resources.gpus` and `resources.mig` matters. Entries earlier in the list will be matched before entries later in the list. MIG devices can also be renamed. For more information, you can read the [documentation](https://docs.google.com/document/d/1dL67t9IqKC2-xqonMi6DV7W2YNZdkmfX7ibB6Jb-qmk).

After modifying the ConfigMap, you should observe that the node is now reporting `nvidia.com/a100` resources, indicating that the configuration was successful.

### 1.3 Clean Up Stale Resources

If you previously installed the Device Plugin and reported `nvidia.com/gpu` type resources, you might notice that `nvidia.com/gpu` is still retained after reconfiguring the Device Plugin. For a detailed discussion on this issue, refer to [here](https://github.com/NVIDIA/k8s-device-plugin/issues/240).

```yaml
allocatable:
  nvidia.com/gpu:   0
  nvidia.com/a100:  8
```

 To clean up stale resources, you can start `kubectl proxy` in one terminal:

```shell
$ kubectl proxy
Starting to serve on 127.0.0.1:8001
```

And in another terminal, run the cleanup script (note `/` needs to be escaped as `~1`):

```bash
#!/bin/bash

# Check if at least one node name is provided
if [ "$#" -lt 1 ]; then
  echo "Usage: $0 <node-name> [<node-name>...]"
  exit 1
fi

# Prepare the JSON patch data
PATCH_DATA=$(cat <<EOF
[
  {"op": "remove", "path": "/status/capacity/nvidia.com~1gpu"}
]
EOF
)

# Iterate over each node name provided as an argument
for NODE_NAME in "$@"
do
  # Execute the PATCH request
  curl --header "Content-Type: application/json-patch+json" \
       --request PATCH \
       --data "$PATCH_DATA" \
       http://127.0.0.1:8001/api/v1/nodes/$NODE_NAME/status

  echo "Patch request sent for node $NODE_NAME"
done
```

Pass the node name and clean up:

```shell
chmod +x ./patch_node_gpu.sh
./patch_node_gpu.sh node1 node2
```

## 2. Configure DCGM Exporter for Pod-Level Monitoring

After changing the GPU resource name, the DCGM Exporter is unable to obtain pod-level GPU usage metrics. 

The reason is that, by default, the DCGM Exporter must fully match the resource name `nvidia.com/gpu` or those with the prefix `nvidia.com/mig-`. For more details, refer to [here](https://github.com/NVIDIA/dcgm-exporter/issues/314).

Starting from version [3.3.7-3.5.0](https://github.com/NVIDIA/dcgm-exporter/releases/tag/3.3.7-3.5.0), you can configure the DCGM Exporter by adding a string array of `nvidia-resource-names` in the command line parameters or environment variables.

```yaml
containers:
  - name: nvidia-dcgm-exporter
    image: 'nvcr.io/nvidia/k8s/dcgm-exporter:3.3.7-3.5.0-ubuntu22.04'
    env:
     ...
      - name: NVIDIA_RESOURCE_NAMES
        value: >-
          nvidia.com/a100,nvidia.com/h100
```

## 3. Configure Volcano to Use the Capacity Scheduling Plugin

After completing the above configuration, you can edit the `volcano-scheduler-configmap` to enable the capacity plugin:

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: volcano-scheduler-configmap
  namespace: volcano-system
data:
  volcano-scheduler.conf: |
    actions: "enqueue, allocate, backfill, reclaim"
    tiers:
    - plugins:
      - name: priority
      - name: gang
        enablePreemptable: false
      - name: conformance
    - plugins:
      - name: drf
        enablePreemptable: false
      - name: predicates
      - name: capacity # add this field and remove proportion plugin.
      - name: nodeorder
      - name: binpack
```

You can customize your configuration according to your needs. For more information, refer to the [documentation](https://github.com/volcano-sh/volcano/blob/release-1.10/docs/user-guide/how_to_use_capacity_plugin.md).