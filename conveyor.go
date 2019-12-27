package conveyor

import (
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
)

const (
	noneIndex = "_none_index"

	logBasePath   = "/host"
	stdout        = "stdout"
	envLogEnabled = "CONVEYOR_ENABLED"
	envLogName    = "CONVEYOR_NAME"
	envLogField   = "CONVEYOR_FIELD"
	envLogPath    = "CONVEYOR_PATH"
	eventDestroy  = "destroy"
	eventDie      = "die"
	eventStart    = "start"
	eventRestart  = "restart"
)

// Conveyor 负责监听 dockerd 产生的容器事件，并收集容器相关信息
type Conveyor struct {
	name string
	// docker client instance
	dc     *client.Client
	porter Porter
}

// ContainerInfo 容器详细信息，给 porter 提供调度依据
type ContainerInfo struct {
	ID      string
	Name    string
	Env     map[string]string
	Labels  map[string]string
	LogPath []string
}

type EnvInfo struct {
	envs   map[string]string
	labels map[string]string
	paths  map[string][]string
}

var (
	DefaultDockerClientOpts = []client.Opt{client.WithVersion("1.39")}

	// Kubernetes 往 docker label 注入的相关变量
	KubernetesLabels = map[string]string{
		"io.kubernetes.pod.namespace":  "kubernetes_pod_namespace",
		"io.kubernetes.pod.name":       "kubernetes_pod_name",
		"io.kubernetes.container.name": "kubernetes_container_name",
	}
)

// NewConveyor 生成 *Conveyor 实例
func NewConveyor(name string, dockerClientOpts ...client.Opt) *Conveyor {
	// conveyor 实例名称，用于在集中群多 conveyor 实例中做区分
	if name == "" {
		name = os.Getenv(envLogName)
	}

	if len(dockerClientOpts) == 0 {
		dockerClientOpts = DefaultDockerClientOpts
	}
	dc, err := client.NewClientWithOpts(dockerClientOpts...)
	if err != nil {
		logrus.Fatalf("new docker client error: %+v", err)
	}
	return &Conveyor{dc: dc, name: name}
}

// RegisterPorter 注册 porter
func (c *Conveyor) RegisterPorter(porter Porter) {
	c.porter = porter
}

func (c *Conveyor) isNeedLog(container *ContainerInfo) bool {
	return container.Env[envLogEnabled] == "true" && container.Env[envLogName] == c.name
}

func (c *Conveyor) splitEnv(env string) (string, string) {
	kv := strings.SplitN(env, "=", 2)
	if len(kv) < 2 {
		return "", ""
	}
	return strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
}

func (c *Conveyor) copyLabels(lbs map[string]string) map[string]string {
	m := make(map[string]string)
	for k, v := range lbs {
		m[k] = v
	}
	return m
}

func (c *Conveyor) extractEnv(envContent []string) EnvInfo {
	envs := make(map[string]string)
	labels := make(map[string]string)
	paths := make(map[string][]string)

	for _, env := range envContent {
		ek, ev := c.splitEnv(env)
		if ek == "" && ev == "" {
			continue
		}
		switch ek {
		case envLogField:
			for _, field := range strings.Split(ev, ",") {
				fk, fv := c.splitEnv(field)
				if fk != "" && fv != "" {
					labels[fk] = fv
				}
			}
			continue
		case envLogPath:
			for _, path := range strings.Split(ev, ";") {
				idxPath := strings.Split(path, ":")
				switch len(idxPath) {
				case 1:
					paths[noneIndex] = append(paths[noneIndex], strings.TrimSpace(idxPath[0]))
				case 2:
					idx, lp := idxPath[0], idxPath[1]
					for _, finalPath := range strings.Split(lp, ",") {
						paths[idx] = append(paths[idx], strings.TrimSpace(finalPath))
					}
				}
			}
			continue
		}
		envs[ek] = ev
	}
	return EnvInfo{envs: envs, labels: labels, paths: paths}
}

func (c *Conveyor) getContainerInfo(containerID string) ([]*ContainerInfo, error) {
	info, err := c.dc.ContainerInspect(context.TODO(), containerID)
	if err != nil {
		return nil, err
	}

	envInfo := c.extractEnv(info.Config.Env)
	envs := envInfo.envs
	labels := envInfo.labels

	for k, v := range info.Config.Labels {
		if KubernetesLabels[k] != "" {
			labels[KubernetesLabels[k]] = v
		}
	}

	containers := make([]*ContainerInfo, 0)
	isExist := make(map[string]bool)
	for idx, paths := range envInfo.paths {
		// idx => input
		lbs := c.copyLabels(labels)
		lpath := make([]string, 0)

		switch idx {
		case noneIndex:
			lb := labels["kubernetes_container_name"]
			if lb == "" {
				lbs["index"] = info.ContainerJSONBase.Name[1:]
			}
		default:
			lbs["index"] = idx
		}

		for _, p := range paths {
			if p == stdout {
				lp := path.Join(logBasePath, info.LogPath)
				if isExist[lp] {
					continue
				}
				lpath = append(lpath, path.Join(logBasePath, info.LogPath))
				isExist[lp] = true
				continue
			}

			for _, mountPoint := range info.Mounts {
				baseDir := filepath.Dir(p)
				if len(baseDir) < len(mountPoint.Destination) {
					continue
				}

				if mountPoint.Destination == baseDir[:len(mountPoint.Destination)] {
					finalPath := path.Join(logBasePath, mountPoint.Source+p[len(mountPoint.Destination):])
					if isExist[finalPath] {
						continue
					}
					lpath = append(lpath, finalPath)
					isExist[finalPath] = true
				}
			}
		}

		containers = append(containers, &ContainerInfo{
			ID:      containerID,
			Name:    info.Name,
			Env:     envs,
			Labels:  lbs,
			LogPath: lpath,
		})
	}

	if len(containers) == 0 {
		containers = append(containers, &ContainerInfo{})
	}

	return containers, nil
}

func (c *Conveyor) list() []*ContainerInfo {
	logrus.Info("EVENT[LIST]: containers")
	containers, err := c.dc.ContainerList(context.TODO(), types.ContainerListOptions{})
	if err != nil {
		logrus.Fatalf("list containers error: %+v", err)
	}

	infos := make([]*ContainerInfo, 0)
	for _, container := range containers {
		cs, err := c.getContainerInfo(container.ID)
		if err != nil {
			logrus.Warnf("get container: %s info error: %+v", container.ID[:8], err)
			continue
		}
		for _, info := range cs {
			if c.isNeedLog(info) {
				infos = append(infos, info)
			}
		}
	}

	return infos
}

func (c *Conveyor) watch() {
	logrus.Info("EVENT[WATCH]: containers")
	filter := filters.NewArgs(filters.KeyValuePair{Key: "type", Value: "container"})
	options := types.EventsOptions{Filters: filter}

	ctx := context.TODO()
	eventMsg, eventErr := c.dc.Events(ctx, options)

	for {
		select {
		case msg := <-eventMsg:
			if err := c.processEvent(msg); err != nil {
				logrus.Warnf("process event: %s error: %+v", msg.Action, err)
			}
		case err := <-eventErr:
			logrus.Warnf("watch event error: %+v", err)
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return
			}
			eventMsg, eventErr = c.dc.Events(ctx, options)
		}
	}
}

func (c *Conveyor) processEvent(e events.Message) error {
	containers, err := c.getContainerInfo(e.Actor.ID)
	if err != nil {
		return err
	}

	if !c.isNeedLog(containers[0]) {
		return nil
	}

	for _, container := range containers {
		switch e.Action {
		case eventStart, eventRestart:
			if err := c.porter.Create(container); err != nil {
				return err
			}
		case eventDestroy, eventDie:
			if err := c.porter.Delete(container); err != nil {
				return err
			}
		}
	}
	return nil
}

// Run Conveyor 入口，负责同时运行 porter 实体进程和监听进程
func (c *Conveyor) Run() {
	containers := c.list()
	if err := c.porter.List(containers); err != nil {
		logrus.Fatalf("porter list-ops error: %+v", err)
	}

	// start porter process
	go c.porter.Run()
	// watch docker event
	go c.watch()

	forever := make(chan struct{})
	<-forever
}
