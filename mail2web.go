package main

import (
	"io/fs"
	"log"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/beego/beego/v2/server/web"
	"github.com/fsnotify/fsnotify"
)

var (
	includedDirs                                    []string
	onlyNumbersRegex                                = regexp.MustCompile("^\\d+$")
	referenceRegex                                  = regexp.MustCompile("<([^>]+)")
	backReferences, children                        map[string]map[string]bool
	mailPaths                                       map[string]string
	backReferencesLock, childrenLock, mailPathsLock sync.RWMutex
	mailDir                                         string
	updates                                         chan update
)

func parseBackreferences(field string) (result map[string]bool) {
	result = make(map[string]bool)
	match := referenceRegex.FindAllStringSubmatch(field, -1)
	for _, reference := range match {
		result[reference[1]] = true
	}
	return
}

type update struct {
	delete     bool
	messageId  string
	references map[string]bool
}

func isEligibleMailPath(path string) bool {
	return onlyNumbersRegex.MatchString(filepath.Base(path))
}

func processMail(path string) (update update) {
	if !isEligibleMailPath(path) {
		return
	}
	file, err := os.Open(path)
	check(err)
	defer func() {
		err := file.Close()
		check(err)
	}()
	message, err := mail.ReadMessage(file)
	if err != nil {
		log.Println(err)
		return
	}
	match := referenceRegex.FindStringSubmatch(message.Header.Get("Message-ID"))
	if len(match) < 2 {
		log.Println(path, "has invalid Message-ID")
		return
	}
	update.messageId = match[1]
	raw_references := message.Header.Get("References")
	if raw_references != "" {
		update.references = parseBackreferences(raw_references)
	}
	return
}

func init() {
	mailDir = os.Getenv("MAILDIR")
	if mailDir == "" {
		mailDir = "/var/lib/mails"
	}
	includedDirs = strings.Split(os.Getenv("MAIL_FOLDERS"), ",")
	backReferences = make(map[string]map[string]bool)
	children = make(map[string]map[string]bool)
	mailPaths = make(map[string]string)
	updates = make(chan update, 1000_000)
	go processUpdates()
	populateGlobalMaps()
	setUpWatcher()
}

func processUpdates() {
	for update := range updates {
		if update.delete {
			backReferencesLock.Lock()
			delete(backReferences, update.messageId)
			backReferencesLock.Unlock()
			childrenLock.Lock()
			for _, currentChildren := range children {
				delete(currentChildren, update.messageId)
			}
			childrenLock.Unlock()
		} else {
			backReferencesLock.Lock()
			backReferences[update.messageId] = update.references
			backReferencesLock.Unlock()
			for reference, _ := range update.references {
				childrenLock.RLock()
				_, ok := children[reference]
				childrenLock.RUnlock()
				childrenLock.Lock()
				if !ok {
					children[reference] = make(map[string]bool)
				}
				children[reference][update.messageId] = true
				childrenLock.Unlock()
			}
		}
	}
}

func populateGlobalMaps() {
	paths := make(chan string)
	var workersWaitGroup sync.WaitGroup
	for i := 0; i < runtime.NumCPU()*2; i++ {
		workersWaitGroup.Add(1)
		go func() {
			for path := range paths {
				if update := processMail(path); update.messageId != "" {
					mailPathsLock.Lock()
					mailPaths[update.messageId] = path
					mailPathsLock.Unlock()
					if len(update.references) > 0 {
						updates <- update
					}
				}
			}
			workersWaitGroup.Done()
		}()
	}
	for _, dir := range includedDirs {
		currentDir := filepath.Join(mailDir, dir)
		err := filepath.WalkDir(currentDir,
			func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if path != currentDir {
						return filepath.SkipDir
					}
					return nil
				}
				paths <- path
				return nil
			})
		check(err)
	}
	close(paths)
	workersWaitGroup.Wait()
}

func setUpWatcher() {
	watcher, err := fsnotify.NewWatcher()
	check(err)

	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op&fsnotify.Create == fsnotify.Create {
					if update := processMail(event.Name); update.messageId != "" {
						log.Println("WATCHER: created file:", event.Name)
						mailPathsLock.Lock()
						mailPaths[update.messageId] = event.Name
						mailPathsLock.Unlock()
						if len(update.references) > 0 {
							updates <- update
						}
					}
				} else if event.Op&fsnotify.Remove == fsnotify.Remove ||
					event.Op&fsnotify.Rename == fsnotify.Rename {
					if isEligibleMailPath(event.Name) {
						var messageId string
						mailPathsLock.RLock()
						for currentMessageId, path := range mailPaths {
							if path == event.Name {
								messageId = currentMessageId
								break
							}
						}
						mailPathsLock.RUnlock()
						if messageId != "" {
							log.Println("WATCHER: deleted file:", event.Name)
							mailPathsLock.Lock()
							delete(mailPaths, messageId)
							mailPathsLock.Unlock()
							updates <- update{
								delete:    true,
								messageId: messageId}
						}
					}
				}
			case err := <-watcher.Errors:
				check(err)
			}
		}
	}()

	for _, folder := range includedDirs {
		absDir := filepath.Join(mailDir, folder)
		err = watcher.Add(absDir)
		check(err)
	}
}

func main() {
	web.Run()
}
