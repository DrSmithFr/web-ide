package docker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/pod/internal/execx"
)

// fake answers commands by their joined argv.
type fake struct {
	out map[string]string
	has bool
	ran []string
}

func (f *fake) Start([]string, string) (execx.Process, error) { return nil, errors.New("no") }
func (f *fake) StartPTY([]string, string, int, int) (execx.PTY, error) {
	return nil, errors.New("no")
}
func (f *fake) Has(string) bool { return f.has }
func (f *fake) Output(_ context.Context, argv []string, _ string) ([]byte, error) {
	k := strings.Join(argv, " ")
	f.ran = append(f.ran, k)
	if o, ok := f.out[k]; ok {
		if strings.HasPrefix(o, "ERR:") {
			return nil, errors.New(o[4:])
		}
		return []byte(o), nil
	}
	return nil, errors.New("unexpected: " + k)
}

func TestParsePorts(t *testing.T) {
	got := parsePorts("0.0.0.0:8080->80/tcp, [::]:8080->80/tcp, 127.0.0.1:5433->5432/tcp, 9000/udp")
	want := []Port{{Published: 8080, Target: 80, Protocol: "tcp"}, {IP: "127.0.0.1", Published: 5433, Target: 5432, Protocol: "tcp"}, {Target: 9000, Protocol: "udp"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ports = %+v", got)
	}
}

func TestStack(t *testing.T) {
	f := &fake{has: true, out: map[string]string{
		"docker compose --profile dev config --services": "web\ndb\nworker\n",
		"docker compose --profile dev config --format json": `{"name":"demo","services":{}}`,
		"docker compose --profile dev ps -a --format json": `{"ID":"b1","Name":"demo-web-1","Image":"nginx","State":"running","Status":"Up 1 minute","Project":"demo","Service":"web","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"},{"URL":"::","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}
{"ID":"a1","Name":"demo-db-1","Image":"postgres","State":"exited","Status":"Exited (0)","Project":"demo","Service":"db","Publishers":[]}
`,
	}}
	st, err := New(f, "/p", func(string) bool { return true }).Stack(context.Background(), []string{"dev"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "demo" || !reflect.DeepEqual(st.Services, []string{"db", "web", "worker"}) || len(st.Containers) != 2 {
		t.Fatalf("stack = %+v", st)
	}
	web := st.Containers[1]
	if web.Service != "web" || !reflect.DeepEqual(web.Ports, []Port{{Published: 8080, Target: 80, Protocol: "tcp"}}) {
		t.Fatalf("web = %+v", web)
	}
	// Older Compose versions print one JSON array.
	f.out["docker compose ps -a --format json"] = `[{"ID":"a1","Name":"x-db-1","State":"running","Service":"db"}]`
	f.out["docker compose config --services"] = "db"
	f.out["docker compose config --format json"] = `{"name":"x"}`
	if st, err = New(f, "/p", nil).Stack(context.Background(), nil); err != nil || len(st.Containers) != 1 {
		t.Fatalf("array output: %+v %v", st, err)
	}
}

func TestStatus(t *testing.T) {
	exists := func(p string) bool { return p == "/p/docker-compose.yml" }
	if st := New(&fake{}, "/p", exists).Status(context.Background()); st.Available || st.Err == nil || st.ComposeFile != "docker-compose.yml" {
		t.Fatalf("without docker: %+v", st)
	}
	f := &fake{has: true, out: map[string]string{
		"docker version --format {{.Server.Version}}": "ERR:permission denied while trying to connect to the Docker daemon socket",
	}}
	if st := New(f, "/p", exists).Status(context.Background()); st.Available || !strings.Contains(st.Err.Error(), "permission denied") {
		t.Fatalf("daemon refused: %+v", st)
	}
	f.out["docker version --format {{.Server.Version}}"] = "27.0.1"
	f.out["docker compose version --short"] = "2.29.0"
	f.out["docker compose config --profiles"] = "debug\n"
	if st := New(f, "/p", exists).Status(context.Background()); !st.Available || !st.Compose || !reflect.DeepEqual(st.Profiles, []string{"debug"}) {
		t.Fatalf("status = %+v", st)
	}
}

func TestActions(t *testing.T) {
	f := &fake{has: true, out: map[string]string{"docker compose up -d web": "", "docker compose stop": "", "docker rm -f abc": ""}}
	d := New(f, "/p", nil)
	ctx := context.Background()
	if d.Compose(ctx, nil, "start", "web") != nil || d.Compose(ctx, nil, "stop", "") != nil || d.Container(ctx, "remove", "abc") != nil {
		t.Fatalf("ran %v", f.ran)
	}
	if d.Compose(ctx, nil, "rm -rf", "") == nil || d.Container(ctx, "exec", "abc") == nil {
		t.Fatal("unknown actions accepted")
	}
}

func TestDisk(t *testing.T) {
	f := &fake{has: true, out: map[string]string{
		"docker system df --format json": `{"Active":"1","Reclaimable":"10MB (2%)","Size":"424MB","TotalCount":"2","Type":"Images"}
{"Active":"0","Reclaimable":"0B","Size":"0B","TotalCount":"1","Type":"Local Volumes"}`,
		"docker system df -v --format json": `{"Images":[{"ID":"sha256:b0f9560a2de083e2cc","Repository":"postgres","Tag":"17-alpine","Size":"424MB","CreatedSince":"2 weeks ago","Containers":"1"}],
"Volumes":[{"Name":"91c98efa","Size":"0B","Links":"0","Labels":"com.docker.volume.anonymous="},{"Name":"demo_data","Size":"12MB","Links":"1","Labels":"com.docker.compose.project=demo"}]}`,
		"docker builder prune -a -f": "ID\nabc\nTotal:\t1.5GB\n",
		"docker volume prune -a -f":  "ERR:unknown shorthand flag: 'a' in -a",
		"docker volume prune -f":     "Deleted Volumes:\nx\n\nTotal reclaimed space: 12MB\n",
	}}
	d := New(f, "/p", nil)
	disk, err := d.Disk(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disk.Usage) != 2 || disk.Usage[0].Total != 2 || disk.Images[0].ID != "b0f9560a2de0" || disk.Images[0].Containers != 1 {
		t.Fatalf("disk = %+v", disk)
	}
	if disk.Volumes[0].Name != "demo_data" || !disk.Volumes[1].Anonymous {
		t.Fatalf("named volumes first: %+v", disk.Volumes)
	}
	if got, err := d.Prune(context.Background(), "buildCache"); err != nil || got != "1.5GB" {
		t.Fatalf("build cache: %q %v", got, err)
	}
	// An older Docker without volume prune -a.
	if got, err := d.Prune(context.Background(), "volumes"); err != nil || got != "12MB" {
		t.Fatalf("volumes: %q %v", got, err)
	}
	if _, err := d.Prune(context.Background(), "system"); err == nil {
		t.Fatal("unknown prune accepted")
	}
}
