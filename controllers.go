package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beego/beego/v2/server/web"
	"github.com/jhillyerd/enmime"
	"golang.org/x/net/html"
)

type typeHashID = hashID

func check(e error) {
	if e != nil {
		logger.Panic(e)
	}
}

// getLogin takes the value of the HTTP “Authorization” header and returns the
// login name found in it.
func getLogin(authHeader string) (login string) {
	components := strings.Split(authHeader, " ")
	if len(components) != 2 || components[0] != "Basic" {
		logger.Panic("Invalid Authorization header " + authHeader)
	}
	rawField, err := base64.StdEncoding.DecodeString(components[1])
	check(err)
	field := string(rawField)
	components = strings.SplitN(field, ":", 2)
	if len(components) != 2 {
		logger.Panic("Invalid Authorization login+password string: " + field)
	}
	return components[0]
}

// getBodyNode is taken from https://stackoverflow.com/a/38855264/188108 and a
// helper for getBody().  It extracts the <body> element from the given HTML
// tree, or nil if it wasn’t found.
func getBodyNode(root *html.Node) (*html.Node, error) {
	var body *html.Node
	var crawler func(*html.Node)
	crawler = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "body" {
			body = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			crawler(child)
		}
	}
	crawler(root)
	if body != nil {
		return body, nil
	}
	return nil, errors.New("Missing <body> in the node tree")
}

// getBody returns everything between <body>…</body> in the given HTML
// document, or the empty string it it wasn’t found.  It is needed to embed
// HTML mails in an HTML document.
//
// BUG(bronger): We don’t do security sanitisation of the HTML here,
// e.g. removing all JavaScript, or preventing CSS to leak to the surrounding
// document.
func getBody(htmlDocument string) (string, error) {
	root, err := html.Parse(strings.NewReader(htmlDocument))
	if err != nil {
		return "", err
	}
	bodyNode, err := getBodyNode(root)
	if err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	writer := io.Writer(&buffer)
	for child := bodyNode.FirstChild; child != nil; child = child.NextSibling {
		err := html.Render(writer, child)
		check(err)
	}
	return buffer.String(), nil
}

func extractMessageID(rawHeader string) messageID {
	match := referenceRegex.FindStringSubmatch(rawHeader)
	if len(match) < 2 {
		return ""
	}
	return messageID(match[1])
}

// findThreadRoot returns the hash ID of the root element of the thread the
// given mail appears in.  If there is no thread, the hash ID of the given mail
// is returned.  It returns the empty string if that hash ID cannot be
// extracted, or if the thread has a depth larger than 100.
func findThreadRoot(m *enmime.Envelope) (root hashID) {
	messageID := extractMessageID(m.GetHeader("Message-ID"))
	if messageID == "" {
		return ""
	}
	var stepBack func(hashID, int) (hashID, int)
	stepBack = func(hashID hashID, depth int) (root hashID, rootDepth int) {
		if depth > 100 {
			return
		}
		references := backReferences[hashID]
		if len(references) == 0 {
			return hashID, depth
		}
		for id, _ := range references {
			root_, rootDepth_ := stepBack(id, depth+1)
			if rootDepth_ > rootDepth {
				root, rootDepth = root_, rootDepth_
			}
		}
		return
	}
	root, _ = stepBack(messageIDToHashID(messageID), 1)
	return root
}

// threadNode represents one mail in a nested thread.  All members are
// expotable because they are needed in the templates.
type threadNode struct {
	MessageID     messageID
	From, Subject string
	RootURL       string
	Link          template.URL
	Children      []*threadNode
}

// decodeRFC2047 returns the given raw mail header (RFC-2047-encoded and
// quoted-printable) to a proper string.
func decodeRFC2047(header string) string {
	dec := new(mime.WordDecoder)
	result, err := dec.DecodeHeader(header)
	if err == nil {
		return result
	} else {
		logger.Println("RFC2047 decoding error:", err)
		logger.Println("Header was:", header)
		return header
	}
}

// threadNodeByHashID returns the given message as a single threadNode,
// i.e. the Children are not yet populated.  It handles the case the the hashID
// points to a fake thread root, i.e. a mail that is references to by other
// mails, but that is not part of the mail archive.
func threadNodeByHashID(hashID hashID) *threadNode {
	mailPathsLock.RLock()
	path := mailPaths[hashID]
	mailPathsLock.RUnlock()
	if path == "" {
		return &threadNode{
			From:    "unknown",
			Subject: "unknown (Hash-ID: " + string(hashID) + ")",
			RootURL: rootURL,
		}
	}
	file, err := os.Open(path)
	check(err)
	defer func() {
		err := file.Close()
		check(err)
	}()
	message, err := mail.ReadMessage(file)
	var from, subject string
	if err == nil {
		from = decodeRFC2047(message.Header.Get("From"))
		subject = decodeRFC2047(message.Header.Get("Subject"))
	} else {
		from = "unknown"
		subject = "unknown"
	}
	messageID := extractMessageID(message.Header.Get("Message-ID"))
	return &threadNode{
		messageID,
		from,
		subject,
		rootURL,
		"",
		nil,
	}
}

// buildThread returns the thread to the given root hash ID as a nested
// structure of threadNode’s.
func buildThread(root hashID) (rootNode *threadNode) {
	rootNode = threadNodeByHashID(root)
	childrenLock.RLock()
	root_children := children[root]
	childrenLock.RUnlock()
	children := make(map[hashID]bool)
	for child, _ := range root_children {
		children[child] = true
	}
	for child, _ := range root_children {
		grandChild := false
		for backReference, _ := range backReferences[child] {
			if children[backReference] {
				grandChild = true
				break
			}
		}
		if grandChild {
			continue
		}
		childNode := buildThread(child)
		if childNode != nil {
			// FixMe: Check that mail is not newer than origin
			rootNode.Children = append(rootNode.Children, childNode)
		}
	}
	sort.SliceStable(rootNode.Children, func(i, j int) bool {
		hashID_i := messageIDToHashID(rootNode.Children[i].MessageID)
		hashID_j := messageIDToHashID(rootNode.Children[j].MessageID)
		timestampsLock.RLock()
		before := timestamps[hashID_i].Before(timestamps[hashID_j])
		timestampsLock.RUnlock()
		return before
	})
	return
}

func messageIDtoURL(messageID messageID) string {
	return strings.ReplaceAll(string(messageID), "/", ">")
}

func messageIDfromURL(urlComponent string) messageID {
	return messageID(strings.ReplaceAll(urlComponent, ">", "/"))
}

// finalizeThread walks through a thread and removes the links (which is
// identical to the hash ID since this is the only elements in the URL path)
// from the node the hash ID of which matches the given one.  The reason is
// that when displaying the thread in the browser, the current email should not
// be hyperlinked.
func finalizeThread(messageID messageID, originHashID hashID, thread *threadNode) *threadNode {
	if thread.MessageID == messageID {
		thread.Link = ""
	} else if hashMessageID(thread.MessageID) == originHashID {
		thread.Link = template.URL(originHashID)
	} else {
		thread.Link = template.URL(fmt.Sprintf("%v/%v", originHashID, messageIDtoURL(thread.MessageID)))
	}
	for _, child := range thread.Children {
		finalizeThread(messageID, originHashID, child)
	}
	return thread
}

func readMail(mailPath string) (message *enmime.Envelope, threadRoot hashID, err error) {
	file, err := os.Open(mailPath)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	check(err)
	defer func() {
		err := file.Close()
		check(err)
	}()
	message, err = enmime.ReadEnvelope(file)
	check(err)
	threadRoot = findThreadRoot(message)
	return
}

func readOriginMail(controller *web.Controller) (hashID hashID, message *enmime.Envelope, threadRoot hashID) {
	hashID = typeHashID(controller.Ctx.Input.Param(":hash"))
	mailPathsLock.RLock()
	mailPath := mailPaths[hashID]
	mailPathsLock.RUnlock()
	message, threadRoot, err := readMail(mailPath)
	if err != nil {
		controller.Abort("404")
	}
	return
}

func pathToLink(path string) string {
	prefix, id := filepath.Split(path)
	_, folder := filepath.Split(strings.TrimSuffix(prefix, "/"))
	return folder + "/" + id
}

type MainController struct {
	web.Controller
}

// Controller for viewing a particular email.
func (this *MainController) Get() {
	messageID := messageIDfromURL(this.Ctx.Input.Param(":messageid"))
	var (
		hashID, threadRoot, originHashID hashID
		message                          *enmime.Envelope
	)
	if messageID == "" {
		hashID, message, threadRoot = readOriginMail(&this.Controller)
		originHashID = hashID
		messageID = extractMessageID(message.GetHeader("Message-ID"))
	} else {
		var originThreadRoot typeHashID
		originHashID, _, originThreadRoot = readOriginMail(&this.Controller)
		hashID = messageIDToHashID(messageID)
		mailPathsLock.RLock()
		mailPath := mailPaths[hashID]
		mailPathsLock.RUnlock()
		var err error
		message, threadRoot, err = readMail(mailPath)
		if err != nil {
			this.Abort("404")
		}
		if originThreadRoot != threadRoot {
			this.Abort("403")
		}
		// FixMe: Check that mail is not newer than origin
	}
	this.Data["hash"] = hashID
	if threadRoot != "" {
		this.Data["thread"] = finalizeThread(messageID, originHashID, buildThread(threadRoot))
	}
	this.TplName = "index.tpl"
	this.Data["rooturl"] = rootURL
	this.Data["from"] = message.GetHeader("From")
	this.Data["subject"] = message.GetHeader("Subject")
	this.Data["to"] = message.GetHeader("To")
	this.Data["date"] = message.GetHeader("Date")
	this.Data["text"] = message.Text
	mailPathsLock.RLock()
	path := mailPaths[hashID]
	mailPathsLock.RUnlock()
	this.Data["link"] = pathToLink(path)
	body, err := getBody(message.HTML)
	check(err)
	this.Data["html"] = template.HTML(body)
	var attachments []string
	for _, currentAttachment := range message.Attachments {
		attachments = append(attachments, currentAttachment.FileName)
	}
	this.Data["attachments"] = attachments
}

type AttachmentController struct {
	web.Controller
}

// Controller for downloading mail attachments.
func (this *AttachmentController) Get() {
	_, message, _ := readOriginMail(&this.Controller)
	index, err := strconv.Atoi(this.Ctx.Input.Param(":index"))
	check(err)
	this.Ctx.Output.Header("Content-Disposition",
		fmt.Sprintf("attachment; filename=\"%v\"", message.Attachments[index].FileName))
	this.Ctx.Output.Header("Content-Type", message.Attachments[index].ContentType)
	err = this.Ctx.Output.Body(message.Attachments[index].Content)
	check(err)
}

// filterHeaders reads the specified mail, removed headers that should not be
// shared with external due to technical or privacy reasons, and returns the
// result.  It panics whenever something wents wrong, as it assumes that the
// basic checks (e.g. that the mail file exists) have been made already.
func filterHeaders(hashID hashID) []byte {
	mailPathsLock.RLock()
	file, err := os.Open(mailPaths[hashID])
	mailPathsLock.RUnlock()
	check(err)
	defer func() {
		err := file.Close()
		check(err)
	}()
	scanner := bufio.NewScanner(file)
	var lines [][]byte
	const (
		inHeader = iota
		inDeletion
		inBody
	)
	state := inHeader
	for scanner.Scan() {
		line := make([]byte, len(scanner.Bytes()))
		copy(line, scanner.Bytes())
		if state != inBody {
			if len(line) == 0 {
				state = inBody
			} else {
				if state == inHeader {
					allLower := bytes.ToLower(line)
					for _, header := range [...]string{"Gcc", "Received", "Sender", "Return-Path",
						"X-Envelope-From", "Envelope-From", "Envelope-To", "Delivered-To",
						"X-Gnus-Mail-Source", "X-From-Line", "Face", "X-Draft-From"} {
						allLowerKey := strings.ToLower(header) + ":"
						if bytes.HasPrefix(allLower, []byte(allLowerKey)) {
							state = inDeletion
							goto Ignore
						}
					}
				} else {
					if bytes.HasPrefix(line, []byte(" ")) {
						goto Ignore
					} else {
						state = inHeader
					}
				}
			}
		}
		lines = append(lines, line)
	Ignore:
	}
	err = scanner.Err()
	check(err)
	return append(bytes.Join(lines, []byte("\r\n")), []byte("\r\n")...)
}

type SendController struct {
	web.Controller
}

// Controller for getting the current email being sent to the logged-in person.
func (this *SendController) Get() {
	loginName := getLogin(this.Ctx.Input.Header("Authorization"))
	emailAddress := getEmailAddress(loginName)
	if emailAddress == "" {
		logger.Panicf("email address of %v not found", loginName)
	}
	hashID := hashID(this.Ctx.Input.Param(":hash"))
	mailBody := filterHeaders(hashID)
	err := smtp.SendMail("postfix:587", nil, "bronger@physik.rwth-aachen.de",
		[]string{emailAddress}, mailBody)
	check(err)
	this.Data["hash"] = hashID
	this.Data["address"] = emailAddress
	this.TplName = "sent.tpl"
	this.Data["rooturl"] = rootURL
}

type MyMailsController struct {
	web.Controller
}

// Controller for searching for mail by message ID/getting an emails by its message ID
func (this *MyMailsController) Get() {
	loginName := getLogin(this.Ctx.Input.Header("Authorization"))
	emailAddress := getEmailAddress(loginName)
	if emailAddress == "" {
		logger.Panicf("email address of %v not found", loginName)
	}
	var rows []mailInfo
	mailsByAddressLock.RLock()
	for _, mailInfo := range mailsByAddress[emailAddress] {
		rows = append(rows, mailInfo)
	}
	mailsByAddressLock.RUnlock()
	logger.Println(len(rows))
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Timestamp.After(rows[j].Timestamp)
	})
	limit := len(rows)
	for i, mail := range rows {
		if time.Since(mail.Timestamp) > thirtyDays {
			limit = i
			break
		}
	}
	this.Data["rows"] = rows[:limit]
	this.TplName = "my_mails.tpl"
	this.Data["rooturl"] = rootURL
}

type HealthController struct {
	web.Controller
}

// Controller for the /healthz endpoint.
func (this *HealthController) Get() {
	err := this.Ctx.Output.Body([]byte{})
	check(err)
}
