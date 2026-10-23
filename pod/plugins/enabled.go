package plugins

import (
	"encoding/json"
	"path"
	"slices"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/pod/fsx"
	"github.com/DrSmithFr/web-ide/pod/idedir"
)

const fileName = "plugins.json"

// file is .ide/plugins.json: the built-in plugins the user disabled on the project (every
// other one is enabled, so a new built-in shows on every project) and the external plugins the
// project uses, by id with the address of their repository (off unless listed: each pod
// installs its own).
type file struct {
	Disabled []string          `json:"disabled"`
	External map[string]string `json:"external,omitempty"`
}

// Choices are the plugins chosen on a project: the built-ins disabled, the externals used.
type Choices struct {
	Disabled []string
	External map[string]string
}

func load(fs fsx.FS, ideDir string) (file, error) {
	f := file{Disabled: []string{}}
	data, err := fs.Read(path.Join(ideDir, fileName))
	if err != nil {
		if _, serr := fs.Stat(path.Join(ideDir, fileName)); serr != nil {
			return f, nil
		}
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, i18n.Errorf("invalid .ide/plugins.json: %v", err)
	}
	if f.Disabled == nil {
		f.Disabled = []string{}
	}
	return f, nil
}

func save(fs fsx.FS, ideDir string, f file) error {
	if err := idedir.Ensure(fs, ideDir); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(f, "", "  ")
	return fs.Write(path.Join(ideDir, fileName), append(data, '\n'))
}

// LoadChoices reads the choices of a .ide folder (none when the file is missing).
func LoadChoices(fs fsx.FS, ideDir string) (Choices, error) {
	f, err := load(fs, ideDir)
	return Choices{Disabled: f.Disabled, External: f.External}, err
}

// LoadDisabled reads the plugins disabled in a .ide folder (none when the file is missing).
func LoadDisabled(fs fsx.FS, ideDir string) ([]string, error) {
	f, err := load(fs, ideDir)
	if err != nil {
		return nil, err
	}
	return f.Disabled, nil
}

// SetEnabled enables or disables a built-in plugin in a .ide folder and returns the choices.
func SetEnabled(fs fsx.FS, ideDir, id string, enabled bool) (Choices, error) {
	f, err := load(fs, ideDir)
	if err != nil {
		return Choices{}, err
	}
	f.Disabled = slices.DeleteFunc(f.Disabled, func(x string) bool { return x == id })
	if !enabled {
		f.Disabled = append(f.Disabled, id)
	}
	slices.Sort(f.Disabled)
	return Choices{Disabled: f.Disabled, External: f.External}, save(fs, ideDir, f)
}

// SetExternal adds an external plugin to a project (url its repository) or takes it out ("").
func SetExternal(fs fsx.FS, ideDir, id, url string) (Choices, error) {
	f, err := load(fs, ideDir)
	if err != nil {
		return Choices{}, err
	}
	if url == "" {
		delete(f.External, id)
	} else {
		if f.External == nil {
			f.External = map[string]string{}
		}
		f.External[id] = url
	}
	if len(f.External) == 0 {
		f.External = nil
	}
	return Choices{Disabled: f.Disabled, External: f.External}, save(fs, ideDir, f)
}
