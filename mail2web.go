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
	excludedDirs = [...]string{"spam-old", "spam", "drafts", "archive", "bogus", "junk", "trash", "sent", "news-sent",
		"RSS", "spam-junk", "wilson-postmaser", "wilson-postmaster", "wilson-rejected", "spam-reports"}
	onlyNumbersRegex                                = regexp.MustCompile("\\d+$")
	referenceRegex                                  = regexp.MustCompile("<([^>]+)")
	backReferences, children                        map[string][]string
	mailPaths                                       map[string]string
	backReferencesLock, childrenLock, mailPathsLock sync.RWMutex
	mailDir                                         string
)

func parseBackreferences(field string) (result []string) {
	result = make([]string, 0)
	match := referenceRegex.FindAllStringSubmatch(field, -1)
	for _, reference := range match {
		result = append(result, reference[1])
	}
	return
}

type update struct {
	messageId  string
	references []string
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
}

func main() {
	paths := make(chan string)
	updates := make(chan update, 1000_000)
	mailPaths = make(map[string]string)
	var workersWaitGroup sync.WaitGroup
	for i := 0; i < runtime.NumCPU()*2; i++ {
		workersWaitGroup.Add(1)
		go func() {
			for path := range paths {
				update := processMail(path)
				if update.messageId != "" {
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
	go func() {
		err := filepath.WalkDir(mailDir,
			func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					for _, dir := range excludedDirs {
						if dir == d.Name() {
							return filepath.SkipDir
						}
					}
					return nil
				}
				paths <- path
				return nil
			})
		check(err)
		close(paths)
		workersWaitGroup.Wait()
		close(updates)
	}()
	backReferences = make(map[string][]string)
	children = make(map[string][]string)
	for update := range updates {
		backReferences[update.messageId] = update.references
		for _, reference := range update.references {
			item, ok := children[reference]
			if !ok {
				item = make([]string, 0, 1)
			}
			children[reference] = append(item, update.messageId)
		}
	}

	web.Run()
}
