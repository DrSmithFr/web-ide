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

// file is .ide/plugins.json: the plugins the user disabled on the project. Every other plugin
// is enabled, so a new built-in shows on every project.
type file struct {
	Disabled []string `json:"disabled"`
}

// LoadDisabled reads the plugins disabled in a .ide folder (none when the file is missing).
func LoadDisabled(fs fsx.FS, ideDir string) ([]string, error) {
	data, err := fs.Read(path.Join(ideDir, fileName))
	if err != nil {
		if _, serr := fs.Stat(path.Join(ideDir, fileName)); serr != nil {
			return []string{}, nil
		}
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, i18n.Errorf("invalid .ide/plugins.json: %v", err)
	}
	if f.Disabled == nil {
		f.Disabled = []string{}
	}
	return f.Disabled, nil
}

// SetEnabled enables or disables a plugin in a .ide folder and returns the disabled list.
func SetEnabled(fs fsx.FS, ideDir, id string, enabled bool) ([]string, error) {
	list, err := LoadDisabled(fs, ideDir)
	if err != nil {
		return nil, err
	}
	list = slices.DeleteFunc(list, func(x string) bool { return x == id })
	if !enabled {
		list = append(list, id)
	}
	slices.Sort(list)
	if err := idedir.Ensure(fs, ideDir); err != nil {
		return nil, err
	}
	data, _ := json.MarshalIndent(file{Disabled: list}, "", "  ")
	return list, fs.Write(path.Join(ideDir, fileName), append(data, '\n'))
}
