package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
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
	Addresses map[string]string
	Groups    map[string]struct {
		Members, Threads, Mails map[string]bool
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
	return permissions.Addresses[loginName]
}

func isAllowed(loginName, folder, id string, threadRoot string) (allowed bool) {
	allowed = false
	path := folder + "/" + id
	if loginName == permissions.Admin {
		allowed = true
	} else {
		for _, group := range permissions.Groups {
			if group.Members[loginName] && (group.Threads[threadRoot] || group.Mails[path]) {
				allowed = true
				break
			}
		}
	}
	if allowed {
		logger.Println(fmt.Sprintf("granted %v access to %v/%v", loginName, folder, id))
	} else {
		logger.Println(fmt.Sprintf("denied %v access to %v/%v", loginName, folder, id))
	}
	return allowed
}

func hashMessageId(messageId string) string {
	hasher := sha256.New()
	hasher.Write(secretKey)
	hasher.Write([]byte(messageId))
	return base64.URLEncoding.EncodeToString(hasher.Sum(nil))[:10]
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
