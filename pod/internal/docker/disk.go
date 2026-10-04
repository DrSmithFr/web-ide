package docker

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// Usage is one line of docker system df: images, containers, local volumes, build cache.
type Usage struct {
	Type        string `json:"type"`
	Total       int    `json:"total"`
	Active      int    `json:"active"`
	Size        string `json:"size"`
	Reclaimable string `json:"reclaimable"`
}

type Image struct {
	ID         string `json:"id"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Size       string `json:"size"`
	Created    string `json:"created"`
	Containers int    `json:"containers"`
}

type Volume struct {
	Name      string `json:"name"`
	Size      string `json:"size"`
	Links     int    `json:"links"`
	Anonymous bool   `json:"anonymous"`
}

type Disk struct {
	Usage   []Usage  `json:"usage"`
	Images  []Image  `json:"images"`
	Volumes []Volume `json:"volumes"`
}

// Disk reads the space used by Docker (docker system df, then its detailed form).
func (d *Docker) Disk(ctx context.Context) (*Disk, error) {
	out, err := d.run.Output(ctx, []string{"docker", "system", "df", "--format", "json"}, "")
	if err != nil {
		return nil, err
	}
	rows, err := jsonLines[struct{ Type, TotalCount, Active, Size, Reclaimable string }](out)
	if err != nil {
		return nil, err
	}
	disk := &Disk{Usage: []Usage{}, Images: []Image{}, Volumes: []Volume{}}
	for _, r := range rows {
		disk.Usage = append(disk.Usage, Usage{Type: r.Type, Total: atoi(r.TotalCount), Active: atoi(r.Active), Size: r.Size, Reclaimable: r.Reclaimable})
	}
	out, err = d.run.Output(ctx, []string{"docker", "system", "df", "-v", "--format", "json"}, "")
	if err != nil {
		return nil, err
	}
	var v struct {
		Images []struct {
			ID, Repository, Tag, Size, CreatedSince, Containers string
		}
		Volumes []struct {
			Name, Size, Links, Labels string
		}
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, err
	}
	for _, im := range v.Images {
		id := strings.TrimPrefix(im.ID, "sha256:")
		if len(id) > 12 {
			id = id[:12]
		}
		disk.Images = append(disk.Images, Image{ID: id, Repository: im.Repository, Tag: im.Tag, Size: im.Size, Created: im.CreatedSince, Containers: atoi(im.Containers)})
	}
	for _, vol := range v.Volumes {
		disk.Volumes = append(disk.Volumes, Volume{Name: vol.Name, Size: vol.Size, Links: atoi(vol.Links),
			Anonymous: strings.Contains(vol.Labels, "com.docker.volume.anonymous")})
	}
	sort.SliceStable(disk.Volumes, func(i, j int) bool { return !disk.Volumes[i].Anonymous && disk.Volumes[j].Anonymous })
	return disk, nil
}

var pruneArgs = map[string][]string{
	"containers":       {"container", "prune", "-f"},
	"danglingImages":   {"image", "prune", "-f"},
	"images":           {"image", "prune", "-a", "-f"},
	"anonymousVolumes": {"volume", "prune", "-f"},
	"volumes":          {"volume", "prune", "-a", "-f"},
	"buildCache":       {"builder", "prune", "-a", "-f"},
}

// Prune removes unused objects of one kind and returns the space freed, as docker prints it.
func (d *Docker) Prune(ctx context.Context, what string) (string, error) {
	args, ok := pruneArgs[what]
	if !ok {
		return "", i18n.Errorf("unknown action %s", what)
	}
	out, err := d.run.Output(ctx, append([]string{"docker"}, args...), "")
	if err != nil && what == "volumes" && strings.Contains(err.Error(), "unknown shorthand flag: 'a'") {
		// Before Docker 23, volume prune removed every unused volume, named ones included.
		out, err = d.run.Output(ctx, []string{"docker", "volume", "prune", "-f"}, "")
	}
	if err != nil {
		return "", err
	}
	return reclaimed(string(out)), nil
}

// reclaimed reads "Total reclaimed space: 1.2GB" (or "Total: 1.2GB" for the build cache).
func reclaimed(out string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		for _, p := range []string{"Total reclaimed space:", "Total:"} {
			if strings.HasPrefix(l, p) {
				return strings.TrimSpace(l[len(p):])
			}
		}
	}
	return "0B"
}

// RemoveObject removes an image or a volume (not used by a container: docker refuses otherwise).
func (d *Docker) RemoveObject(ctx context.Context, kind, id string) error {
	var argv []string
	switch kind {
	case "image":
		argv = []string{"docker", "image", "rm", id}
	case "volume":
		argv = []string{"docker", "volume", "rm", id}
	default:
		return i18n.Errorf("unknown action %s", kind)
	}
	_, err := d.run.Output(ctx, argv, "")
	return err
}
