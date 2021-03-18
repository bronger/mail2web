package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v2"
)

var (
	permissionsPath string
	secretKey       []byte
)

var permissions struct {
	Admin     string
	Addresses map[string][]string
	Groups    map[string]struct {
		Members        map[string]bool
		Threads, Mails map[hashID]bool
	}
}

// readPermissions reads the permissions.yaml file which resides in the
// mailDir.  Note that we only log parsing errors here instead of treating them
// as fatal errors because it may be that the permissions.yaml is not yet fully
// written.  FWIW, fsnotify fires four “write” notifications if I save the file
// with Emacs or Nano.
func readPermissions() {
	data, err := ioutil.ReadFile(permissionsPath)
	check(err)
	err = yaml.Unmarshal(data, &permissions)
	if err != nil {
		logger.Println("invalid permissions.yaml")
		permissions.Addresses = nil
		permissions.Groups = nil
	} else {
		logger.Println("re-read permissions.yaml")
	}
}

// setUpWatcher starts a goroutine that watches for changes in permissions.yaml
// and re-reads it when necessary.
func setUpPermissionsWatcher() {
	watcher, err := fsnotify.NewWatcher()
	check(err)

	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Name == permissionsPath && (event.Op&fsnotify.Create == fsnotify.Create ||
					event.Op&fsnotify.Write == fsnotify.Write) {
					readPermissions()
				}
			case err := <-watcher.Errors:
				check(err)
			}
		}
	}()

	err = watcher.Add(permissionsPath)
	check(err)
	err = watcher.Add(filepath.Dir(permissionsPath))
	check(err)
}

// getEmailAddress returns the email address of the given user.  If it is not
// found in permissions.yaml, the result is empty.
func getEmailAddress(loginName string) string {
	addresses := permissions.Addresses[loginName]
	if len(addresses) == 0 {
		return ""
	} else {
		return addresses[0]
	}
}

// getEmailAddress returns all email addresses the given user can read.
func getEmailAddresses(loginName string) []string {
	return permissions.Addresses[loginName]
}

// hashMessageID hashes the message ID with a pepper taken from
// SECRET_KEY_PATH.
func hashMessageID(messageID messageID) hashID {
	hasher := sha256.New()
	hasher.Write(secretKey)
	hasher.Write([]byte(messageID))
	return hashID(base64.URLEncoding.EncodeToString(hasher.Sum(nil))[:10])
}

func init() {
	permissionsPath = filepath.Join(mailDir, "permissions.yaml")
	var err error
	secretKeyPath := os.Getenv("SECRET_KEY_PATH")
	if secretKeyPath == "" {
		secretKeyPath = "/var/lib/mail2web_secrets/secret_key"
	}
	secretKey, err = ioutil.ReadFile(secretKeyPath)
	check(err)
	secretKey = bytes.Trim(secretKey, "\t\n\r\f\v ")
	readPermissions()
	setUpPermissionsWatcher()
}
