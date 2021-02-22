package main

import (
	"io/fs"
	"log"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
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

func main() {
	back_references = make(map[string][]string)
	children = make(map[string][]string)
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
			if !only_numbers_regex.MatchString(d.Name()) {
				return nil
			}
			file, err := os.Open(path)
			defer file.Close()
			if err != nil {
				log.Panic(err)
			}
			message, err := mail.ReadMessage(file)
			if err != nil {
				log.Println(err)
				return nil
			}
			match := reference_regex.FindStringSubmatch(message.Header.Get("Message-ID"))
			if len(match) < 2 {
				return nil
			}
			message_id := match[1]
			references := message.Header.Get("References")
			if references != "" {
				parsed_references := parse_backreferences(references)
				back_references[message_id] = parsed_references
				for _, reference := range parsed_references {
					item, ok := children[reference]
					if !ok {
						item = make([]string, 0, 1)
					}
					children[reference] = append(item, message_id)
				}
			}
			return nil
		}); err != nil {
		log.Panic(err)
	}
}
