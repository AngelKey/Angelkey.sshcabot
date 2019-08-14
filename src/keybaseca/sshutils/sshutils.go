package sshutils

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/keybase/bot-ssh-ca/src/keybaseca/botwrapper"

	"github.com/keybase/bot-ssh-ca/src/keybaseca/log"

	"github.com/keybase/bot-ssh-ca/src/keybaseca/config"
	"github.com/keybase/bot-ssh-ca/src/shared"

	"github.com/google/uuid"
)

func GenerateNewSSHKey(filename string, overwrite bool, printPubKey bool) error {
	_, err := os.Stat(filename)
	if err == nil {
		if overwrite {
			err := os.Remove(filename)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("Refusing to overwrite existing key (try with --overwrite-existing-key or FORCE_WRITE=true if you're sure): %s", filename)
		}
	}

	err = generateNewSSHKey(filename)
	if err != nil {
		return err
	}

	if printPubKey {
		bytes, err := ioutil.ReadFile(shared.KeyPathToPubKey(filename))
		if err != nil {
			return err
		}
		fmt.Printf("Generated new public key: \n%s\n", string(bytes))
	}

	return nil
}

func Generate(conf config.Config, overwrite bool, printPubKey bool) error {
	err := GenerateNewSSHKey(conf.GetCAKeyLocation(), overwrite, printPubKey)
	if err == nil {
		log.Log(conf, fmt.Sprintf("Wrote new SSH CA key to %s", conf.GetCAKeyLocation()))
	}
	return err
}

// Get a temporary filename that starts with pattern using ioutil.TempFile
func getTempFilename(pattern string) (string, error) {
	f, err := ioutil.TempFile("", pattern)
	if err != nil {
		return "", err
	}
	tempFilename := f.Name()
	f.Close()
	err = os.Remove(tempFilename)
	if err != nil {
		return "", err
	}
	return tempFilename, nil
}

func ProcessSignatureRequest(conf config.Config, sr shared.SignatureRequest) (resp shared.SignatureResponse, err error) {
	randomUUID, err := uuid.NewRandom()
	if err != nil {
		return
	}
	principals, err := getPrincipals(conf, sr)
	if err != nil {
		return
	}

	// The key ID uniquely identifies the certificate by encoding the UUID of the request, a new UUID, and the username
	// Use both their uuid and our uuid to ensure it is unique
	keyID := sr.UUID + ":" + randomUUID.String() + ":" + sr.Username

	log.Log(conf, fmt.Sprintf("Processing SignatureRequest from user=%s on device='%s' keyID:%s, principals:%s, expiration:%s, pubkey:%s",
		sr.Username, sr.DeviceName, keyID, principals, conf.GetKeyExpiration(), sr.SSHPublicKey))
	signature, err := SignKey(conf.GetCAKeyLocation(), keyID, principals, conf.GetKeyExpiration(), sr.SSHPublicKey)

	return shared.SignatureResponse{SignedKey: signature, UUID: sr.UUID}, nil
}

// Sign an SSH public key with the given data. Do so without any operations that rely on Keybase in order to ensure
// that running `keybaseca sign` works even if Keybase is down.
func SignKey(caKeyLocation, keyID, principals, expiration, publicKey string) (signature string, err error) {
	// Just a little bit of validation to give a nice error message
	if strings.Contains(publicKey, "PRIVATE KEY") {
		return "", fmt.Errorf("SignKey expects a public key (not a private key)")
	}

	// Write the public key to a temporary file
	tempFilename, err := getTempFilename("keybase-ca-signed-key")
	if err != nil {
		return
	}
	err = ioutil.WriteFile(shared.KeyPathToPubKey(tempFilename), []byte(publicKey), 0600)
	if err != nil {
		return
	}

	// Note that we use ssh-keygen rather than Go's builtin SSH library since Go's SSH library does not support ed25519
	// SSH keys.
	cmd := exec.Command("ssh-keygen",
		"-s", caKeyLocation, // The CA key
		"-I", keyID, // A unique key ID
		"-n", principals, // The allowed principals
		"-V", expiration, // The expiration period for the key
		"-N", "", // No password on the key
		shared.KeyPathToPubKey(tempFilename), // The location of the public key
	)
	bytes, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ssh-keygen error: %s (%v)", strings.TrimSpace(string(bytes)), err)
	}

	// Read the certificate from the file
	signatureBytes, err := ioutil.ReadFile(shared.KeyPathToCert(tempFilename))
	if err != nil {
		return
	}

	// Delete the certificate and the pub key from the filesystem
	err = os.Remove(shared.KeyPathToPubKey(tempFilename))
	if err != nil {
		return
	}
	err = os.Remove(shared.KeyPathToCert(tempFilename))
	if err != nil {
		return
	}

	return string(signatureBytes), nil
}

// Get the principals that should be placed in the signed certificate.
// Note that this function is a security boundary since if it was bypassed an
// attacker would be able to provision SSH keys for environments that they should not have access to.
func getPrincipals(conf config.Config, sr shared.SignatureRequest) (string, error) {
	// Start by getting the list of teams the user is in
	api, err := botwrapper.GetKBChat(conf.GetKeybaseHomeDir(), conf.GetKeybasePaperKey(), conf.GetKeybaseUsername())
	if err != nil {
		return "", fmt.Errorf("failed to retrieve the list of teams the user is in: %v", err)
	}
	results, err := api.ListUserMemberships(sr.Username)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve the list of teams the user is in: %v", err)
	}

	// Maps from a team to whether or not the user is in the current team (with writer, admin, or owner permissions)
	teamToMembership := make(map[string]bool)
	for _, result := range results {
		if result.Role != 0 {
			// result.Role == 0 means they are an impicit admin in the team and are not actually a member
			teamToMembership[result.TeamName] = true
		}
	}

	// Iterate through the teams in the config file and use the subteam as the principal
	// if the user is in that subteam
	var principals []string
	for _, team := range conf.GetTeams() {
		result, ok := teamToMembership[team]
		if ok && result {
			principals = append(principals, team)
		}
	}
	return strings.Join(principals, ","), nil
}
