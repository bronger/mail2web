package main

import (
	"io/fs"
	"log"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"

	"github.com/beego/beego/v2/server/web"
)

var (
	includedDirs                                    []string
	onlyNumbersRegex                                = regexp.MustCompile("\\d+$")
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
	messageId  string
	references map[string]bool
}

func processMail(path string) (update update) {
	if !onlyNumbersRegex.MatchString(filepath.Base(path)) {
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
}

func processUpdates() {
	for update := range updates {
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

func main() {
	web.Run()
}
