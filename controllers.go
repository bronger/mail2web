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
	"sort"
	"strconv"
	"strings"

	"github.com/beego/beego/v2/server/web"
	"github.com/jhillyerd/enmime"
	"golang.org/x/net/html"
)

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

// findThreadRoot returns the hash ID of the root element of the thread the
// given mail appears in.  If there is no thread, the hash ID of the given mail
// is returned.  It returns the empty string if that hash ID cannot be
// extracted, or if the thread has a depth larger than 100.
func findThreadRoot(m *enmime.Envelope) (root string) {
	match := referenceRegex.FindStringSubmatch(m.GetHeader("Message-ID"))
	if len(match) < 2 {
		return ""
	}
	hashId := messageIdToHashId(match[1])
	var stepBack func(string, int) (string, int)
	stepBack = func(hashId string, depth int) (root string, rootDepth int) {
		if depth > 100 {
			return
		}
		references := backReferences[hashId]
		if len(references) == 0 {
			return hashId, depth
		}
		for id, _ := range references {
			root_, rootDepth_ := stepBack(id, depth+1)
			if rootDepth_ > rootDepth {
				root, rootDepth = root_, rootDepth_
			}
		}
		return
	}
	root, _ = stepBack(hashId, 1)
	return root
}

// threadNode represents one mail in a nested thread.  All members are
// expotable because they are needed in the templates.
type threadNode struct {
	HashId        string
	From, Subject string
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
		logger.Printf("RFC2047 decoding error:", err)
		return header
	}
}

// threadNodeByHashId returns the given message as a single threadNode,
// i.e. the Children are not yet populated.  It handles the case the the hashId
// points to a fake thread root, i.e. a mail that is references to by other
// mails, but that is not part of the mail archive.
func threadNodeByHashId(hashId string) *threadNode {
	mailPathsLock.RLock()
	path := mailPaths[hashId]
	mailPathsLock.RUnlock()
	if path == "" {
		return &threadNode{
			HashId:  hashId,
			From:    "unknown",
			Subject: "unknown (Hash-ID: " + hashId + ")",
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
	return &threadNode{
		hashId,
		from,
		subject,
		nil,
	}
}

// buildThread returns the thread to the given root hash ID as a nested
// structure of threadNode’s.
func buildThread(root string) (rootNode *threadNode) {
	rootNode = threadNodeByHashId(root)
	childrenLock.RLock()
	root_children := children[root]
	childrenLock.RUnlock()
	children := make(map[string]bool)
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
			rootNode.Children = append(rootNode.Children, childNode)
		}
	}
	timestampsLock.RLock()
	sort.SliceStable(rootNode.Children, func(i, j int) bool {
		return timestamps[rootNode.Children[i].HashId].Before(timestamps[rootNode.Children[j].HashId])
	})
	timestampsLock.RUnlock()
	return
}

// removeCurrentLink walks through a thread and removed the links (which is
// identical to the hash ID sich this is the only elements in the URL path)
// from the node the hash ID of which matches the given one.  The reason is
// that when displaying the thread in the browser, the current email should not
// be hyperlinked.
func removeCurrentLink(hashId string, thread *threadNode) *threadNode {
	if thread.HashId == hashId {
		thread.HashId = ""
	}
	for _, child := range thread.Children {
		removeCurrentLink(hashId, child)
	}
	return thread
}

func readMail(controller *web.Controller) (
	hashId string, message *enmime.Envelope, threadRoot string) {
	hashId = controller.Ctx.Input.Param(":hash")
	mailPathsLock.RLock()
	file, err := os.Open(mailPaths[hashId])
	mailPathsLock.RUnlock()
	if errors.Is(err, fs.ErrNotExist) {
		controller.Abort("404")
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

type MainController struct {
	web.Controller
}

// Controller for viewing a particular email.
func (this *MainController) Get() {
	hashId, message, threadRoot := readMail(&this.Controller)
	this.Data["hash"] = hashId
	this.TplName = "index.tpl"
	if threadRoot != "" {
		this.Data["thread"] = removeCurrentLink(hashId, buildThread(threadRoot))
	}
	this.Data["from"] = message.GetHeader("From")
	this.Data["subject"] = message.GetHeader("Subject")
	this.Data["to"] = message.GetHeader("To")
	this.Data["date"] = message.GetHeader("Date")
	this.Data["text"] = message.Text
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
	_, message, _ := readMail(&this.Controller)
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
func filterHeaders(hashId string) []byte {
	mailPathsLock.RLock()
	file, err := os.Open(mailPaths[hashId])
	mailPathsLock.RUnlock()
	check(err)
	defer func() {
		err := file.Close()
		check(err)
	}()
	scanner := bufio.NewScanner(file)
	var lines [][]byte
	const (
		inHeader   = iota
		inDeletion = iota
		inBody     = iota
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
	hashId := this.Ctx.Input.Param(":hash")
	mailBody := filterHeaders(hashId)
	err := smtp.SendMail("postfix:587", nil, "bronger@physik.rwth-aachen.de",
		[]string{emailAddress}, mailBody)
	check(err)
	err = this.Ctx.Output.Body([]byte{})
	check(err)
}

type MessageIdController struct {
	web.Controller
}

// Controller for getting an emails by its message ID
func (this *MessageIdController) Get() {
	hashIdsLock.RLock()
	hashId, ok := hashIds[this.Ctx.Input.Param(":messageid")]
	hashIdsLock.RUnlock()
	if !ok {
		this.Abort("404")
	}
	this.Redirect("/"+hashId, 303)
}

type HealthController struct {
	web.Controller
}

// Controller for the /healthz endpoint.
func (this *HealthController) Get() {
	err := this.Ctx.Output.Body([]byte{})
	check(err)
}
