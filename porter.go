package conveyor

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"sync"
	"text/template"
	"time"

	"github.com/sirupsen/logrus"
)

type Porter interface {
	List(containers []*ContainerInfo) error
	Create(container *ContainerInfo) error
	Delete(container *ContainerInfo) error
	Run()
}

type FileBeatPorterOpts struct {
	BaseDir       string
	ExecBin       string
	BaseCfgFile   string
	BaseCfgTmpl   string
	CustomCfgFile string
	CustomCfgTmpl string
}

var DefaultFileBeatOpts = &FileBeatPorterOpts{
	BaseDir:     "/etc/filebeat",
	ExecBin:     "filebeat",
	BaseCfgFile: "filebeat.yaml",
	BaseCfgTmpl: `
filebeat.config.inputs:
  enabled: true
  path: /etc/filebeat/configs/*.yaml
  reload.enabled: true
  reload.period: 10s
output.console:
  pretty: true
`,
	CustomCfgFile: "config.tmpl",
	CustomCfgTmpl: `
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
`,
}

func readFileContent(path string) string {
	in, err := os.Open(path)
	if err != nil {
		logrus.Fatalf("open file:[%s] error: %+v", path, err)
	}

	bs, err := ioutil.ReadAll(in)
	if err != nil {
		logrus.Fatalf("read file:[%s] error: %+v", path, err)
	}
	return string(bs)
}

func isFileExist(path string) bool {
	_, err := os.Lstat(path)
	return !os.IsNotExist(err)
}

type FileBeatPorter struct {
	mux        sync.Mutex
	process    *exec.Cmd
	containers []*ContainerInfo
	opts       *FileBeatPorterOpts
}

func NewFileBeatPorter(opts *FileBeatPorterOpts) *FileBeatPorter {
	if opts == nil {
		opts = DefaultFileBeatOpts
	}

	baseCfg := fmt.Sprintf("%s/%s", opts.BaseDir, opts.BaseCfgFile)
	if isFileExist(baseCfg) {
		opts.BaseCfgTmpl = readFileContent(baseCfg)
	}

	customCfg := fmt.Sprintf("%s/configs/%s", opts.BaseDir, opts.CustomCfgFile)
	if isFileExist(customCfg) {
		opts.CustomCfgTmpl = readFileContent(customCfg)
	}
	return &FileBeatPorter{opts: opts}
}

func (p *FileBeatPorter) getBaseCfgFile() string {
	return fmt.Sprintf("%s/%s", p.opts.BaseDir, p.opts.BaseCfgFile)
}

func (p *FileBeatPorter) getCustomCfgFile() string {
	return fmt.Sprintf("%s/configs/config.yaml", p.opts.BaseDir)
}

func (p *FileBeatPorter) getExecBin() string {
	return fmt.Sprintf("%s/%s", p.opts.BaseDir, p.opts.ExecBin)
}

func (p *FileBeatPorter) updateConfig(containers []*ContainerInfo) error {
	tmpl, err := template.New("filebeat").Parse(p.opts.CustomCfgTmpl)
	if err != nil {
		logrus.Fatalf("new template error: %+v", err)
	}

	f, err := os.Create(p.getCustomCfgFile())
	if err != nil {
		logrus.Fatalf("create config file error: %+v", err)
	}
	return tmpl.Execute(f, containers)
}

func (p *FileBeatPorter) List(containers []*ContainerInfo) error {
	p.mux.Lock()
	p.containers = containers
	p.mux.Unlock()
	return p.updateConfig(p.containers)
}

func (p *FileBeatPorter) Create(container *ContainerInfo) error {
	logrus.Infof("EVENT[CREATE]: [CONTAINER ID]: %s [CONTAINER NAME]: %s", container.ID[:8], container.Name)

	p.mux.Lock()
	p.containers = append(p.containers, container)
	p.mux.Unlock()
	return p.updateConfig(p.containers)
}

func (p *FileBeatPorter) Delete(container *ContainerInfo) error {
	logrus.Infof("EVENT[DELETE]: [CONTAINER ID]: %s [CONTAINER NAME]: %s", container.ID[:8], container.Name)

	cs := make([]*ContainerInfo, 0)
	for _, origin := range p.containers {
		if origin.ID != container.ID {
			cs = append(cs, origin)
		}
	}

	p.mux.Lock()
	p.containers = cs
	p.mux.Unlock()
	return p.updateConfig(p.containers)
}

func (p *FileBeatPorter) start() error {
	var err error
	if !isFileExist(p.getBaseCfgFile()) {
		// refer https://www.elastic.co/guide/en/beats/libbeat/7.x/config-file-permissions.html
		if err = ioutil.WriteFile(p.getBaseCfgFile(), []byte(p.opts.BaseCfgTmpl), 0644); err != nil {
			return err
		}
	}

	logrus.Info("EVENT[START]: filebeat process")
	p.process = exec.Command(p.getExecBin(), "-c", p.getBaseCfgFile())
	p.process.Stderr = os.Stderr
	p.process.Stdout = os.Stdout

	if err = p.process.Start(); err != nil {
		return err
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err = p.process.Wait(); err != nil {
			p.process = nil
			return
		}
	}()
	wg.Wait()
	return err
}

func (p *FileBeatPorter) Run() {
	// retry forever
	for {
		if err := p.start(); err != nil {
			logrus.Warnf("restart filebeat: %+v", err)
		}
		time.Sleep(2 * time.Second)
	}
}
