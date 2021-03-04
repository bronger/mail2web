package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v2"
)

var permissionsPath string

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
		log.Print(fmt.Sprintf("granted %v access to %v/%v", loginName, folder, id))
	} else {
		log.Print(fmt.Sprintf("denied %v access to %v/%v", loginName, folder, id))
	}
	return allowed
}

func init() {
	permissionsPath = filepath.Join(mailDir, "permissions.yaml")
	readPermissions()
	setUpPermissionsWatcher()
}
