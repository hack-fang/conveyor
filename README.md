<p align="center">
   <img src="https://user-images.githubusercontent.com/19553554/70031426-34c1d100-15e6-11ea-85bd-79eeaaa1d5aa.png" alt="conveyor logo" width=200 height=200 />
</p>
<h1 align="center">conveyor</h1>

<p align="center">
  <em>Transport log-entity via conveyor. Inspired by <a href="https://github.com/AliyunContainerService/log-pilot">AliyunContainerService/log-pilot</a>.</em>
</p>

> why conveyor? Highly customizable and more details...

[conveyor](https://github.com/chenjiandongx/conveyor) 是采一个负责采集 docker 容器日志的组件，使用 Porter 将特定容器产生的日志输出到指定后端，如 kafka/elasticsearch/redis/...。目前 porter 有:

* [filebeat-porter](https://github.com/elastic/beats/tree/master/filebeat).
* TODO...

## 📝 使用

### 本地开发构建

#### 安装

GOPATH mode
```shell
$ go get -u github.com/chenjiandongx/conveyor/...
```

GOMODULE mode
```shell
require (
  github.com/chenjiandongx/conveyor
)
```

#### 示例 
```golang
package main

import (
	conveyor "github.com/chenjiandongx/conveyor/pkg"
)

func main() {
      // 实例化 porter
      porter := conveyor.NewFileBeatPorter(nil)
      // 实例化 conveyor
      cy := conveyor.NewConveyor("")
      // 将 porter 注册到 conveyor 中
      cy.RegisterPorter(porter)
      // 运行 conveyor
      cy.Run()

}
```

Porter Interface/ ContainerInfo Struct
```golang
type Porter interface {
	List(containers []*ContainerInfo) error
	Create(container *ContainerInfo) error
	Delete(container *ContainerInfo) error
	Run()
}

type ContainerInfo struct {
	ID      string
	Name    string
	Env     map[string]string
	Labels  map[string]string
	LogPath []string
}
```

### 使用 Docker 运行

#### 运行 conveyor

容器启动的时候会优先读取 /etc/filebeat/filebeat.yaml 和 /etc/filebeat/configs/config.tmpl 两个配置文件，不存在则使用默认配置。`${your_varname}` 均为可选参数，非必须。
```shell
$ docker run -d --restart=always --name conveyor \ 
   -v /var/run/docker.sock:/var/run/docker.sock
   -v /:/host:ro
   -v ${your_filebeat_data_dir}:/etc/filebeat/data
   -v ${your_filebeat_base_confile_file}:/etc/filebeat/filebeat.yaml
   -v ${your_filebeat_custom_confile_tmpl}:/etc/filebeat/configs/config.tmpl
   -e CONVEYOR_NAME=${your_conveyor_name}
   chenjiandongx/conveyor:latest
```

默认 /etc/filebeat/filebeat.yaml
```yaml
# 标准 filebeat 配置文件
filebeat.config.inputs:
  enabled: true
  path: /etc/filebeat/configs/*.yaml
  reload.enabled: true
  reload.period: 10s
output.console:
  pretty: true
```

默认 /etc/filebeat/configs/config.tmpl
```yaml
# 标准 golang 模板语言
- type: log
  paths:
  - "/tmp/tmp.log"
{{- range . }}
- type: log
  paths:
  {{- range .LogPath }}
  - "{{ . }}"
  {{- end }}
  fields:
  {{- range $k, $v := .Labels }}
    {{ $k }}: {{ $v }}
  {{- end }}
{{- end }}
```

#### 运行示例容器

启动容器后向 nginx 发送请求再查看 conveyor 的日志，可以看到日志被输出到标准输出。
```shell
$ docker run -d -e CONVEYOR_ENABLED=true -e CONVEYOR_PATH="stdout" --name ngx nginx 
```

容器环境变量

* `CONVEYOR_NAME`: conveyor 实例名称。默认为 ""
* `CONVEYOR_ENABLED`: 是否开启日志追踪，"true" 时开启。默认为 "" 
* `CONVEYOR_FILED`: filebeat.inputs.fields 字段，支持 `,` 分割，如 CONVEYOR_FILED="app=nginx,env=dev"。默认为 "" 
* `CONVEYOR_PATH`: 用户自定义追踪日志路径，支持 `;` 分割，"stdout" 代表采集容器的标准输出。同时也支持指定索引（仅适用于 ES 输出端），索引以 `:` 分割，`CONVEYOR_PATH="tmp-index:/tmp/logs/*.log;stdout-index:stdout"` 表示讲追踪标准日志输出以及 `/tmp/logs/*.log` 路径下的日志，前者在 ES 中的前缀为 `tmp-index` 后者为 `stdout-index`。

### 使用 Kubernetes 运行

使用 DaemonSet 形式部署 conveyor，生产环境请将 /etc/filebeat/data 目录使用 PVC 挂载出来，该目录记录着 filebeat 的消费进度。
```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: conveyor
spec:
  selector:
    matchLabels:
      name: conveyor
  template:
    metadata:
      labels:
        name: conveyor
    spec:
      containers:
      - name: conveyor
        image: chenjiandongx/conveyor:latest
        resources:
          limits:
            memory: 200Mi
          requests:
            cpu: 100m
            memory: 200Mi
        volumeMounts:
        # 挂载 docker.sock 文件，监听 dockerd 事件
        - name: docker-sock
          mountPath: /var/run/docker.sock
        # 挂载 / 路径，只读权限，用于日志收集 
        - name: docker-log
          mountPath: /host
          readOnly: true
        - name: filebeat-config
          mountPath: /etc/filebeat/filebeat.yaml
          subPath: filebeat.yaml
      volumes:
      - name: docker-sock
        hostPath:
          path: /var/run/docker.sock
      - name: docker-log
        hostPath:
          path: /
      - name: filebeat-config
        configMap:
          name: filebeat-config
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: filebeat-config
data:
  filebeat.yaml: |
    setup.ilm.enabled: false
    setup.template.enabled: true
    setup.template.name: filebeat
    setup.template.pattern: filebeat-*
    filebeat.config.inputs:
      enabled: true
      path: /etc/filebeat/configs/*.yaml
      reload.enabled: true
      reload.period: 10s
    # elasticsearch 输出端示例
    output.elasticsearch:
      hosts: ["http://elasticsearch-svc:9200"]
      index: "filebeat-%{[fields.index]}-%{+yyyy.MM.dd}"
```

部署 nginx depolyments 实例

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ngx
spec:
  replicas: 4
  selector:
    matchLabels:
      run: ngx
  template:
    metadata:
      labels:
        run: ngx
    spec:
      containers:
      - image: nginx
        # 可另外挂载空白卷追踪自定义日志文件
        volumeMounts:
         - mountPath: /tmp/logs
           name: tmp-log
        name: ngx
        env:
        - name: CONVEYOR_ENABLED
          value: "true"
        # 定义自定义日志路径
        - name: CONVEYOR_PATH
          value: "tmp-index:/tmp/logs/*.log;stdout-index:stdout"
      # 声明空白卷
      volumes:
      - emptyDir: {}
        name: tmp-log
```

![](https://user-images.githubusercontent.com/19553554/70505132-33eaeb00-1b62-11ea-83ca-c111cd930e2b.png)

## 📃 License

MIT [©chenjiandongx](https://github.com/chenjiandongx)

