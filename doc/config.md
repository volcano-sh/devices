## Config the volcano device plugin binary

The volcano device plugin has a number of options that can be configured. These options can be configured as command line flags, environment variables, or via a config file when launching the device plugin. The following section explains these configurations.

### As command line flags or envvars

| Flag                     | Envvar                  | Default Value   |
|--------------------------|-------------------------|-----------------|
| `--gpu-strategy`         | `$GPU_STRATEGY`         | `"share"`       |
| `--config-file`          | `$CONFIG_FILE`          | `""`            |

when starting volcano-device-plugin.yml, users can specify these parameters by adding args to the container 'volcano-device-plugin'.
For example: 
 - args: ["--gpu-strategy=number"] will let device plugin using the gpu-number strategy

### As a configuration file
```
version: v1
flags:
  GPUStrategy: "number"
```

### Configuration Option Details
**`GPU_STRATEGY`**:
  the desired strategy for exposing GPU devices

  `[number | share ] (default 'share')`

  The `GPU_STRATEGY` option configures the daemonset to be able to expose
  on GPU devices in numbers or sharing mode. More information on what
  these strategies are and how to use it in Volcano can be found in Volcano scheduler.

**`CONFIG_FILE`**:
  point the plugin at a configuration file instead of relying on command line
  flags or environment variables

  `(default '')`

  The order of precedence for setting each option is (1) command line flag, (2)
  environment variable, (3) configuration file. In this way, one could use a
  pre-defined configuration file, but then override the values set in it at
  launch time. 
