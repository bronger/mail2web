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
	"time"

	"github.com/beego/beego/v2/server/web"
	"github.com/fsnotify/fsnotify"
)

var (
	logger           *log.Logger
	includedDirs     []string
	onlyNumbersRegex = regexp.MustCompile("^\\d+$")
	referenceRegex   = regexp.MustCompile("<([^>]+)")
	emailRegex       = regexp.MustCompile("[a-zA-Z0-9.!#$%&'*+\\/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}" +
		"[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*")
	hashIds                                                         map[string]string
	backReferences, children                                        map[string]map[string]bool
	mailPaths                                                       map[string]string
	timestamps                                                      map[string]time.Time
	mailsByAddress                                                  map[string]map[string]mailInfo
	hashIdsLock, mailsByAddressLock                                 sync.RWMutex
	backReferencesLock, childrenLock, mailPathsLock, timestampsLock sync.RWMutex
	mailDir                                                         string
	updates                                                         chan update
)

const thirtyDays = time.Hour * 24 * 30

func messageIdToHashId(messageId string) (hashId string) {
	hashIdsLock.RLock()
	hashId, ok := hashIds[messageId]
	hashIdsLock.RUnlock()
	if !ok {
		hashId = hashMessageId(messageId)
		hashIdsLock.Lock()
		hashIds[messageId] = hashId
		hashIdsLock.Unlock()
	}
	return hashId
}

// parseBackreferences returns the hash IDs mentioned in the given field.  The
// field may be e.g. “Message-ID” or “References”.
func parseBackreferences(field string) (result map[string]bool) {
	result = make(map[string]bool)
	match := referenceRegex.FindAllStringSubmatch(field, -1)
	for _, reference := range match {
		result[messageIdToHashId(reference[1])] = true
	}
	return
}

type mailInfo struct {
	HashId, MessageId string
	From, Subject     string
	Timestamp         time.Time
	references        map[string]bool
}

// This struct is passed through the channel “updates” to a central goroutine
// that processes the updates.  It represents one email.  “references” contains
// the hash IDs in the “References” header field.  “timestamp” contains the
// date of the email.  If “delete” is true, only “hashId” is used and all other
// fields may be left empty.
type update struct {
	delete                        bool
	rawFrom, rawTo, rawCc, rawBcc string
	mailInfo
}

func (update update) getAddresses() (addresses map[string]bool) {
	matches := emailRegex.FindAllStringSubmatch(update.rawFrom, -1)
	matches = append(matches, emailRegex.FindAllStringSubmatch(update.rawTo, -1)...)
	matches = append(matches, emailRegex.FindAllStringSubmatch(update.rawCc, -1)...)
	matches = append(matches, emailRegex.FindAllStringSubmatch(update.rawBcc, -1)...)
	addresses = make(map[string]bool)
	for _, match := range matches {
		addresses[match[0]] = true
	}
	return addresses
}

// isEligibleMailPath returns whether the given path refers to a file that
// mail2web should assume to be an RFC 5322 mail file.  It is simply a filename
// that consists only of numbers.
func isEligibleMailPath(path string) bool {
	return onlyNumbersRegex.MatchString(filepath.Base(path))
}

// processMail reads the RFC 5322 mail file at the given path and returns a
// corresponding “update” object, ready to be sent to the “updates” channel.
// If anything goes wrong, an empty “update” is returned.
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
		logger.Println(err)
		return
	}
	match := referenceRegex.FindStringSubmatch(message.Header.Get("Message-ID"))
	if len(match) < 2 {
		logger.Println(path, "has invalid Message-ID")
		return
	}
	update.MessageId = match[1]
	update.HashId = messageIdToHashId(update.MessageId)
	update.Timestamp, _ = mail.ParseDate(message.Header.Get("Date"))
	raw_references := message.Header.Get("References")
	if raw_references != "" {
		update.references = parseBackreferences(raw_references)
	}
	update.rawFrom = message.Header.Get("From")
	update.rawTo = message.Header.Get("To")
	update.rawCc = message.Header.Get("Cc")
	update.rawBcc = message.Header.Get("Bcc")
	update.From = decodeRFC2047(update.rawFrom)
	update.Subject = decodeRFC2047(message.Header.Get("Subject"))
	return
}

// setupLogging sets up logging into a file.  The file is called
// “mail2web.log”, and the directory is taken from the environment variable
// M2W_LOG_PATH.  If this is not set, Go’s default logger ist used.
func setupLogging() *log.Logger {
	logPath := os.Getenv("M2W_LOG_PATH")
	if logPath == "" {
		return log.Default()
	}
	logFilename := filepath.Join(logPath, "mail2web.log")
	logfile, err := os.OpenFile(logFilename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Panic(err)
	}
	return log.New(logfile, "", log.Lshortfile|log.LstdFlags)
}

func init() {
	logger = setupLogging()
	mailDir = os.Getenv("MAILDIR")
	if mailDir == "" {
		mailDir = "/var/lib/mails"
	}
	includedDirs = strings.Split(os.Getenv("MAIL_FOLDERS"), ",")
	hashIds = make(map[string]string)
	backReferences = make(map[string]map[string]bool)
	children = make(map[string]map[string]bool)
	mailPaths = make(map[string]string)
	mailsByAddress = make(map[string]map[string]mailInfo)
	timestamps = make(map[string]time.Time)
	updates = make(chan update, 1000_000)
}

// processUpdates is a goroutine running for the whole run time of the program.
// It reads from the channel “updates” and updates the global data structures
// “backReferences”, “children”, “mailPaths”, and “timestamps” accordingly.
// To keep those mappings consistent, sending to the “updates” channel should
// be the only may to write to them.  Besides, the channel is faster for
// serialisition than write locks.
func processUpdates() {
	for update := range updates {
		if update.delete {
			backReferencesLock.RLock()
			formerBackReferences, ok := backReferences[update.HashId]
			backReferencesLock.RUnlock()
			if ok {
				backReferencesLock.Lock()
				delete(backReferences, update.HashId)
				backReferencesLock.Unlock()
				childrenLock.Lock()
				for ancestor, _ := range formerBackReferences {
					delete(children[ancestor], update.HashId)
				}
				childrenLock.Unlock()
			}
			timestampsLock.Lock()
			delete(timestamps, update.HashId)
			timestampsLock.Unlock()
		} else {
			backReferencesLock.Lock()
			backReferences[update.HashId] = update.references
			backReferencesLock.Unlock()
			for reference, _ := range update.references {
				childrenLock.RLock()
				_, ok := children[reference]
				childrenLock.RUnlock()
				childrenLock.Lock()
				if !ok {
					children[reference] = make(map[string]bool)
				}
				children[reference][update.HashId] = true
				childrenLock.Unlock()
			}
			timestampsLock.Lock()
			timestamps[update.HashId] = update.Timestamp
			timestampsLock.Unlock()
		}
	}
}

// populateGlobalMaps walks once through all mail files and sends them to the
// “updates” channel.  This routine runs once, at the very beginning of the
// program, to take care of the initial population of the global maps.
func populateGlobalMaps() {
	paths := make(chan string)
	var workersWaitGroup sync.WaitGroup
	for i := 0; i < runtime.NumCPU()*2; i++ {
		workersWaitGroup.Add(1)
		go func() {
			for path := range paths {
				if update := processMail(path); update.HashId != "" {
					mailPathsLock.Lock()
					mailPaths[update.HashId] = path
					mailPathsLock.Unlock()
					if time.Since(update.Timestamp) <= thirtyDays {
						mailsByAddressLock.Lock()
						for address, _ := range update.getAddresses() {
							if mailsByAddress[address] == nil {
								mailsByAddress[address] = make(map[string]mailInfo)
							}
							mailsByAddress[address][update.HashId] = update.mailInfo
						}
						mailsByAddressLock.Unlock()
					}
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

// setUpWatcher starts a goroutine that watches for changes in the mail folders
// and sends them to “updates” accordingly.
func setUpWatcher() {
	watcher, err := fsnotify.NewWatcher()
	check(err)

	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op&fsnotify.Create == fsnotify.Create {
					if update := processMail(event.Name); update.HashId != "" {
						logger.Println("WATCHER: created file:", event.Name)
						mailPathsLock.Lock()
						mailPaths[update.HashId] = event.Name
						mailPathsLock.Unlock()
						mailsByAddressLock.Lock()
						for address, _ := range update.getAddresses() {
							if mailsByAddress[address] == nil {
								mailsByAddress[address] = make(map[string]mailInfo)
							}
							mailsByAddress[address][update.HashId] = update.mailInfo
						}
						mailsByAddressLock.Unlock()
						if len(update.references) > 0 {
							updates <- update
						}
					}
				} else if event.Op&fsnotify.Remove == fsnotify.Remove ||
					event.Op&fsnotify.Rename == fsnotify.Rename {
					if isEligibleMailPath(event.Name) {
						var hashId string
						mailPathsLock.RLock()
						for currentMessageId, path := range mailPaths {
							if path == event.Name {
								hashId = currentMessageId
								break
							}
						}
						mailPathsLock.RUnlock()
						if hashId != "" {
							logger.Println("WATCHER: deleted file:", event.Name)
							mailPathsLock.Lock()
							delete(mailPaths, hashId)
							mailPathsLock.Unlock()
							updates <- update{
								delete:   true,
								mailInfo: mailInfo{HashId: hashId}}
						}
						mailsByAddressLock.Lock()
						for _, mails := range mailsByAddress {
							delete(mails, hashId)
						}
						mailsByAddressLock.Unlock()
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
	go processUpdates()
	setUpWatcher()
	populateGlobalMaps()

	web.Run()
}
