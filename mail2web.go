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
)

var (
	excluded_dirs = [...]string{"spam-old", "spam", "drafts", "archive", "bogus", "junk", "trash", "sent", "news-sent",
		"RSS", "spam-junk", "wilson-postmaser", "wilson-postmaster", "wilson-rejected", "spam-reports"}
	only_numbers_regex        = regexp.MustCompile("\\d+$")
	reference_regex           = regexp.MustCompile("<([^>]+)")
	back_references, children map[string][]string
)

func parse_backreferences(field string) (result []string) {
	result = make([]string, 0)
	match := reference_regex.FindAllStringSubmatch(field, -1)
	for _, reference := range match {
		result = append(result, reference[1])
	}
	return
}

type update struct {
	message_id string
	references []string
}

func process_mail(path string) (update update) {
	if !only_numbers_regex.MatchString(filepath.Base(path)) {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		log.Panic(err)
	}
	defer file.Close()
	message, err := mail.ReadMessage(file)
	if err != nil {
		log.Println(err)
		return
	}
	match := reference_regex.FindStringSubmatch(message.Header.Get("Message-ID"))
	if len(match) < 2 {
		log.Println(path, "has invalid Message-ID")
		return
	}
	update.message_id = match[1]
	raw_references := message.Header.Get("References")
	if raw_references != "" {
		update.references = parse_backreferences(raw_references)
	}
	return
}

func main() {
	paths := make(chan string)
	updates := make(chan update, 1000)
	var workersWaitGroup sync.WaitGroup
	for i := 0; i < runtime.NumCPU()*2; i++ {
		workersWaitGroup.Add(1)
		go func() {
			for path := range paths {
				update := process_mail(path)
				if update.message_id != "" && len(update.references) > 0 {
					updates <- update
				}
			}
			workersWaitGroup.Done()
		}()
	}
	go func() {
		if err := filepath.WalkDir("/home/bronger/Mail",
			func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					for _, dir := range excluded_dirs {
						if dir == d.Name() {
							return filepath.SkipDir
						}
					}
					return nil
				}
				paths <- path
				return nil
			}); err != nil {
			log.Panic(err)
		}
		close(paths)
		workersWaitGroup.Wait()
		close(updates)
	}()
	back_references = make(map[string][]string)
	children = make(map[string][]string)
	for update := range updates {
		back_references[update.message_id] = update.references
		for _, reference := range update.references {
			item, ok := children[reference]
			if !ok {
				item = make([]string, 0, 1)
			}
			children[reference] = append(item, update.message_id)
		}
	}
}
