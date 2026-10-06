package preview

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CLI drives the tailscale command of the machine of the pod.
type CLI struct {
	once sync.Once
	host string
}

func (c *CLI) run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tailscale", args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Host is read once: the DNS name of the machine, when Tailscale runs and serves HTTPS.
func (c *CLI) Host() (string, bool) {
	c.once.Do(func() {
		if _, err := exec.LookPath("tailscale"); err != nil {
			return
		}
		out, err := c.run("status", "--self", "--json")
		if err != nil {
			return
		}
		var st struct {
			Self struct {
				DNSName string
			}
		}
		if json.Unmarshal(out, &st) == nil {
			c.host = strings.TrimSuffix(st.Self.DNSName, ".")
		}
	})
	return c.host, c.host != ""
}

// Serve and Funnel replace what a pod stopped abruptly may have left on the port.
func (c *CLI) Serve(port int, target string) error {
	_ = c.Off(port, false)
	_, err := c.run("serve", "--bg", "--https="+strconv.Itoa(port), target)
	return err
}

func (c *CLI) Funnel(port int, target string) error {
	_ = c.Off(port, true)
	_, err := c.run("funnel", "--bg", "--https="+strconv.Itoa(port), target)
	return err
}

func (c *CLI) Off(port int, funnel bool) error {
	cmd := "serve"
	if funnel {
		cmd = "funnel"
	}
	_, err := c.run(cmd, "--https="+strconv.Itoa(port), "off")
	return err
}
