//go:build linux

package openacp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// The agent sees a copied workspace, public runtime libraries and certificates,
// not the host root, home, Git credentials or managed OpenCode configuration.
const namespaceScript = `
set -eu
root=$1
binary=$2
readonly=$3
mount --make-rprivate /
mount --bind "$binary" "$root/bin/opencode"
mount -o remount,bind,ro "$root/bin/opencode"
mount --bind /usr/bin/setpriv "$root/bin/setpriv"
mount -o remount,bind,ro "$root/bin/setpriv"
mount --bind "$root/bin/rg" "$root/bin/rg"
mount -o remount,bind,ro "$root/bin/rg"
for dir in lib lib64; do
  if [ -d "/$dir" ]; then
    mount --rbind "/$dir" "$root/$dir"
    mount -o remount,bind,ro "$root/$dir"
  fi
done
mount --bind /etc/ssl/certs "$root/etc/ssl/certs"
mount -o remount,bind,ro "$root/etc/ssl/certs"
mount --bind /etc/resolv.conf "$root/etc/resolv.conf"
mount -o remount,bind,ro "$root/etc/resolv.conf"
for dev in null urandom random; do
  mount --bind "/dev/$dev" "$root/dev/$dev"
done
mount -t proc proc "$root/proc"
mount --bind "$root/workspace" "$root/workspace"
if [ "$readonly" = true ]; then
  mount -o remount,bind,ro "$root/workspace"
fi
mount --bind "$root/config" "$root/config"
mount -o remount,bind,ro "$root/config"
chroot "$root" /bin/rg --version >/dev/null
exec chroot "$root" /bin/setpriv --bounding-set=-all --inh-caps=-all --ambient-caps=-all --no-new-privs /bin/opencode acp
`

func sandboxCommand(ctx context.Context, root, binary string, readOnly bool) (*exec.Cmd, error) {
	for _, name := range []string{"unshare", "mount", "chroot", "sh", "setpriv"} {
		if _, err := exec.LookPath(name); err != nil {
			return nil, fmt.Errorf("openacp: Linux isolation requires %s: %w", name, err)
		}
	}
	ripgrep, err := exec.LookPath("rg")
	if err != nil {
		return nil, fmt.Errorf("openacp: Linux isolation requires native ripgrep: %w", err)
	}
	search, err := os.ReadFile(ripgrep)
	if err != nil {
		return nil, fmt.Errorf("openacp: read native ripgrep: %w", err)
	}
	for _, dir := range []string{"bin", "lib", "lib64", "etc/ssl/certs", "dev", "proc", "tmp", "home", "config", "data", "cache", "state", "workspace"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"bin/opencode", "bin/setpriv", "etc/resolv.conf", "dev/null", "dev/urandom", "dev/random"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(root, "bin/rg"), search, 0700); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "unshare", "--user", "--map-root-user", "--mount", "--pid", "--fork",
		"--kill-child=KILL", "--", "sh", "-c", namespaceScript, "taxion-openacp", root, binary, fmt.Sprint(readOnly))
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.Env = []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/home", "TMPDIR=/tmp",
		"XDG_CONFIG_HOME=/config", "XDG_DATA_HOME=/data", "XDG_CACHE_HOME=/cache", "XDG_STATE_HOME=/state",
		"OPENCODE_DISABLE_PROJECT_CONFIG=true", "OPENCODE_DISABLE_AUTOUPDATE=true",
		"OPENCODE_DISABLE_SHARE=true", "OPENCODE_DISABLE_DEFAULT_PLUGINS=true",
	}
	return cmd, nil
}
