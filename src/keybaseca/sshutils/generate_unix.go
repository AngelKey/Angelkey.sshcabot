// +build !windows

package sshutils

import (
	"fmt"
	"os/exec"
	"strings"
)

// Generate a new SSH key. Places the private key at filename and the public key at filename.pub.
// On unix, we use ed25519 keys since they may be more secure (and are smaller). The go crypto ssh library
// does not support ed25519 keys so we use ssh-keygen in order to generate the key.
func generateNewSSHKey(filename string) error {
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", filename, "-m", "PEM", "-N", "")
	bytes, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh-keygen failed: %s (%v)", strings.TrimSpace(string(bytes)), err)
	}
	return nil
}