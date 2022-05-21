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
	"sync"
	textTemplate "text/template"
	"time"

	"github.com/beego/beego/v2/server/web"
	"github.com/jhillyerd/enmime"
	"golang.org/x/net/html"
	"golang.org/x/text/encoding/charmap"
)

type typeHashID = hashID

var requestMailTemplate *textTemplate.Template

const (
	accessSingle = iota
	accessDirect
	accessOlder
	accessFull
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

// substituteImgSrcs replaces in-place the src attributes of <img> tags, if
// they start with “cid:”.  In this case, “hashID/img/” is prepended to the URL
// so that they are valid URL in the browser that can be responded to by the
// server.
func substituteImgSrcs(root *html.Node, urlPrefix, queryString string) {
	var crawler func(*html.Node)
	crawler = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "img" {
			for i, attribute := range node.Attr {
				if attribute.Key == "src" && strings.HasPrefix(attribute.Val, "cid:") {
					node.Attr[i].Val = urlPrefix + "/img/" + attribute.Val + queryString
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			crawler(child)
		}
	}
	crawler(root)
}

// getBody returns everything between <body>…</body> in the given HTML
// document, or the empty string it it wasn’t found.  It is needed to embed
// HTML mails in an HTML document.
//
// BUG(bronger): We don’t do security sanitisation of the HTML here,
// e.g. removing all JavaScript, or preventing CSS to leak to the surrounding
// document.
func getBody(htmlDocument string, urlPrefix, queryString string) (string, error) {
	root, err := html.Parse(strings.NewReader(htmlDocument))
	if err != nil {
		return "", err
	}
	bodyNode, err := getBodyNode(root)
	if err != nil {
		return "", err
	}
	substituteImgSrcs(bodyNode, urlPrefix, queryString)
	var buffer bytes.Buffer
	writer := io.Writer(&buffer)
	for child := bodyNode.FirstChild; child != nil; child = child.NextSibling {
		err := html.Render(writer, child)
		check(err)
	}
	return buffer.String(), nil
}

// extractMessageID takes the value of the “Message-ID” header field of an
// email and strips whitespace and the <…> brackets.  It returns the pure
// message ID, or the empty string if no ID was found.
func extractMessageID(rawHeader string) messageID {
	match := referenceRegex.FindStringSubmatch(rawHeader)
	if len(match) < 2 {
		return ""
	}
	return messageID(match[1])
}

// collectThread returns the hash IDs of all mails in the thread the given hash
// ID is part of.
func collectThread(hashID hashID) (hashIDs map[hashID]bool) {
	var flood func(typeHashID, map[typeHashID]bool)
	flood = func(node typeHashID, visitedNodes map[typeHashID]bool) {
		visitedNodes[node] = true
		childrenLock.RLock()
		for child := range children[node] {
			if !visitedNodes[child] {
				flood(child, visitedNodes)
			}
		}
		childrenLock.RUnlock()
		backReferencesLock.RLock()
		for ancestor := range backReferences[node] {
			if !visitedNodes[ancestor] {
				flood(ancestor, visitedNodes)
			}
		}
		backReferencesLock.RUnlock()
	}
	hashIDs = make(map[typeHashID]bool)
	flood(hashID, hashIDs)
	return
}

// collectSubthread returns the hash IDs of all mails in the thread the given
// hash ID is root of.
func collectSubthread(hashID hashID) (hashIDs map[hashID]bool) {
	var flood func(typeHashID, map[typeHashID]bool)
	flood = func(node typeHashID, visitedNodes map[typeHashID]bool) {
		visitedNodes[node] = true
		childrenLock.RLock()
		for child := range children[node] {
			if !visitedNodes[child] {
				flood(child, visitedNodes)
			}
		}
		childrenLock.RUnlock()
	}
	hashIDs = make(map[typeHashID]bool)
	flood(hashID, hashIDs)
	return
}

// existingMail returns whether the given mail exists on the filesystem or not.
// If not, it is only found in the back references of existing mails.
func existingMail(hashID hashID) (existing bool) {
	mailPathsLock.RLock()
	existing = mailPaths[hashID] != ""
	mailPathsLock.RUnlock()
	return
}

var cachedRoots sync.Map

// findThreadRoot returns the hash ID of the root element of the thread the
// given mail appears in.  If there is no thread, the hash ID of the given mail
// is returned.  It returns the empty string if that hash ID cannot be
// extracted, or if the thread has a depth larger than 100.
func findThreadRoot(m *enmime.Envelope) (root hashID) {
	messageID := extractMessageID(m.GetHeader("Message-ID"))
	if messageID == "" {
		return ""
	}
	hashID := messageIDToHashID(messageID)
	if raw, ok := cachedRoots.Load(hashID); ok {
		return raw.(typeHashID)
	}
	nodes := collectThread(hashID)
	visitedNodes := make(map[typeHashID]bool, len(nodes))
	var threadSize int
	var existingCandidates, nonexistingCandidates map[typeHashID]bool
	for node := range nodes {
		if !visitedNodes[node] {
			nodeThread := collectSubthread(node)
			for node_ := range nodeThread {
				visitedNodes[node_] = true
			}
			nodeThreadSize := len(nodeThread)
			if nodeThreadSize > threadSize {
				threadSize = nodeThreadSize
				existingCandidates = make(map[typeHashID]bool)
				nonexistingCandidates = make(map[typeHashID]bool)
				if existingMail(node) {
					existingCandidates[node] = true
				} else {
					nonexistingCandidates[node] = true
				}
			} else if nodeThreadSize == threadSize {
				if existingMail(node) {
					existingCandidates[node] = true
				} else {
					nonexistingCandidates[node] = true
				}
			}
		}
	}
	var candidates []typeHashID
	if len(existingCandidates) > 0 {
		for node := range existingCandidates {
			candidates = append(candidates, node)
		}
		if len(candidates) != len(existingCandidates) {
			logger.Panic("Duplicate found in existingCandidates")
		}
	} else {
		for node := range nonexistingCandidates {
			candidates = append(candidates, node)
		}
		if len(candidates) != len(nonexistingCandidates) {
			logger.Panic("Duplicate found in nonexistingCandidates")
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
	root = candidates[0]
	for node := range nodes {
		if existingMail(node) {
			cachedRoots.Store(node, root)
		}
	}
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
// quoted-printable) as a proper string.
func decodeRFC2047(header string) string {
	decoder := mime.WordDecoder{
		func(charset string, input io.Reader) (io.Reader, error) {
			switch charset {
			case "windows-1252", "cp1252":
				return charmap.Windows1252.NewDecoder().Reader(input), nil
			case "iso-8859-2":
				return charmap.ISO8859_2.NewDecoder().Reader(input), nil
			case "iso-8859-15":
				return charmap.ISO8859_15.NewDecoder().Reader(input), nil
			}
			return nil, fmt.Errorf("unknown encoding %v", header)
		},
	}
	result, err := decoder.DecodeHeader(header)
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
			Subject: "unknown",
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
func buildThread(root, originHashID hashID, accessMode int) (rootNode *threadNode, originIncluded bool) {
	originIncluded = root == originHashID
	rootNode = threadNodeByHashID(root)
	childrenLock.RLock()
	root_children := children[root]
	childrenLock.RUnlock()
	children := make(map[hashID]bool)
	for child := range root_children {
		children[child] = true
	}
	for child := range root_children {
		if accessMode != accessFull {
			timestampsLock.RLock()
			after := timestamps[child].After(timestamps[originHashID])
			timestampsLock.RUnlock()
			if after {
				continue
			}
		}
		grandChild := false
		for backReference := range backReferences[child] {
			if children[backReference] {
				grandChild = true
				break
			}
		}
		if grandChild {
			continue
		}
		childNode, originIncludedInChild := buildThread(child, originHashID, accessMode)
		if childNode != nil {
			originIncluded = originIncluded || originIncludedInChild
			rootNode.Children = append(rootNode.Children, childNode)
		}
	}
	if accessMode == accessDirect && len(rootNode.Children) == 0 && root != originHashID {
		return nil, false
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

// messageIDtoURL returns a URL-safe string representation of the given message
// ID.  It still needs proper percent escaping, it just handles slashes in
// message IDs, which never seem to be esscaped by Go’s template engine.  See,
// e.g., <https://github.com/golang/go/issues/3659>.
func messageIDtoURL(messageID messageID) string {
	return strings.ReplaceAll(string(messageID), "/", ">")
}

// messageIDfromURL is the inverse of messageIDtoURL.  It takes a URL component
// (i.e., something that must never contain slashes) and returns the original
// message ID.
func messageIDfromURL(urlComponent string) messageID {
	return messageID(strings.ReplaceAll(urlComponent, ">", "/"))
}

// finalizeThread walks through a thread and removes the links (which is
// identical to the hash ID since this is the only elements in the URL path)
// from the node the hash ID of which matches the given one.  The reason is
// that when displaying the thread in the browser, the current email should not
// be hyperlinked.
func finalizeThread(messageID messageID, originHashID hashID, thread *threadNode, queryString template.URL) *threadNode {
	if thread.MessageID == "" || thread.MessageID == messageID {
		thread.Link = ""
	} else {
		if hashMessageID(thread.MessageID, "") == originHashID {
			thread.Link = template.URL(originHashID)
		} else {
			thread.Link = template.URL(fmt.Sprintf("%v/%v", originHashID, messageIDtoURL(thread.MessageID)))
		}
		thread.Link += queryString
	}
	for _, child := range thread.Children {
		finalizeThread(messageID, originHashID, child, queryString)
	}
	return thread
}

// readMail reads an RFC 5322 file and returns it as a mail object.  The
// returned error is non-nil only if the mail file could not be found.
func readMail(mailPath string) (message *enmime.Envelope, err error) {
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
	return
}

// readOriginMail is a helper for getMailAndThreadRoot.  It returns hash ID,
// message object, thread root ID, access mode (only one mail, whole thread
// etc.) and token for the *origin* mail, i.e. the one given in the hash
// component of the URL (in contrast to the optional message ID component).  It
// may trigger an HTTP 404 if the mail file was not found, and an HTTP 403 if a
// tokenFull is given but invalid.
func readOriginMail(controller *web.Controller) (
	hashID hashID, message *enmime.Envelope, threadRoot hashID, messageID messageID, accessMode int, token string) {
	hashID = typeHashID(controller.Ctx.Input.Param(":hash"))
	mailPathsLock.RLock()
	mailPath := mailPaths[hashID]
	mailPathsLock.RUnlock()
	message, err := readMail(mailPath)
	if err != nil {
		controller.Abort("404")
	}
	messageID = extractMessageID(message.GetHeader("Message-ID"))
	accessMode = accessSingle
	scanForToken := func(name string) bool {
		token = controller.GetString("token" + strings.Title(name))
		if token != "" {
			if token != string(hashMessageID(messageID, name)) {
				logger.Printf(
					"Denied access because token %v is invalid for message ID %v and access mode %v",
					token, messageID, name)
				controller.Abort("403")
			}
			return true
		}
		return false
	}
	switch true {
	case scanForToken("direct"):
		accessMode = accessDirect
	case scanForToken("older"):
		accessMode = accessOlder
	case scanForToken("full"):
		accessMode = accessFull
	}
	if accessMode != accessSingle {
		threadRoot = findThreadRoot(message)
	}
	return
}

// getMailAndThreadRoot encapsulates common code used in some controllers.  It
// returns data for both the concrete (given by the message ID in the URL) and
// the original mail (given by the hash in the URL).  It checks whether a token
// – if given – is valid and may trigger HTTP 4… errors.
func getMailAndThreadRoot(controller *web.Controller) (accessMode int, token string, messageID messageID,
	hashID, threadRoot, originHashID hashID, message *enmime.Envelope, link string) {
	messageID = messageIDfromURL(controller.Ctx.Input.Param(":messageid"))
	if messageID == "" {
		hashID, message, threadRoot, messageID, accessMode, token = readOriginMail(controller)
		originHashID = hashID
		link = string(hashID)
	} else {
		var originThreadRoot typeHashID
		originHashID, _, originThreadRoot, _, accessMode, token = readOriginMail(controller)
		if accessMode == accessSingle {
			logger.Println("Denied access because message ID parameter is forbidden for single access mode")
			controller.Abort("403")
		}
		hashID = messageIDToHashID(messageID)
		mailPathsLock.RLock()
		mailPath := mailPaths[hashID]
		mailPathsLock.RUnlock()
		var err error
		message, err = readMail(mailPath)
		if err != nil {
			controller.Abort("404")
		}
		if accessMode != accessSingle {
			threadRoot = findThreadRoot(message)
			if originThreadRoot != threadRoot {
				mailPathsLock.RLock()
				originThreadRootPath := mailPaths[originThreadRoot]
				threadRootPath := mailPaths[threadRoot]
				mailPathsLock.RUnlock()
				if originThreadRootPath == "" {
					originThreadRootPath = "<invalid hash ID!>"
				}
				if threadRootPath == "" {
					threadRootPath = "<invalid hash ID!>"
				}
				logger.Printf(
					"Denied access because message ID %v and hash ID %v are different threads: %v, %v",
					messageID, originHashID, originThreadRootPath, threadRootPath)
				controller.Abort("403")
			}
		}
		link = fmt.Sprintf("%v/%v", originHashID, messageIDtoURL(messageID))
	}
	return
}

// pathToLink generates a nice title for the mail Web page.  It extracts the
// “folder/id” from the given full mail path.
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
	accessMode, token, messageID, hashID, threadRoot, originHashID, message, link :=
		getMailAndThreadRoot(&this.Controller)
	var queryString template.URL
	if accessMode != accessSingle {
		var key string
		switch accessMode {
		case accessDirect:
			key = "tokenDirect"
		case accessOlder:
			key = "tokenOlder"
		case accessFull:
			key = "tokenFull"
		}
		queryString = template.URL("?" + key + "=" + token)
		this.Data["queryString"] = queryString
	}
	if threadRoot != "" {
		thread, originIncluded := buildThread(threadRoot, originHashID, accessMode)
		if !originIncluded {
			logger.Printf("Denied access because selected mail %v is not included in allowed thread", messageID)
			this.Abort("403")
		}
		this.Data["thread"] = finalizeThread(messageID, originHashID, thread, queryString)
	}
	this.TplName = "index.tpl"
	this.Data["rooturl"] = rootURL
	this.Data["link"] = template.URL(link)
	this.Data["from"] = message.GetHeader("From")
	this.Data["subject"] = message.GetHeader("Subject")
	this.Data["to"] = message.GetHeader("To")
	this.Data["cc"] = message.GetHeader("Cc")
	this.Data["date"] = message.GetHeader("Date")
	this.Data["text"] = message.Text
	mailPathsLock.RLock()
	path := mailPaths[hashID]
	mailPathsLock.RUnlock()
	this.Data["name"] = pathToLink(path)
	body, err := getBody(message.HTML, rootURL+"/"+link, string(queryString))
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
	_, _, _, _, _, _, message, _ := getMailAndThreadRoot(&this.Controller)
	index, err := strconv.Atoi(this.Ctx.Input.Param(":index"))
	check(err)
	this.Ctx.Output.Header("Content-Disposition",
		fmt.Sprintf("attachment; filename=\"%v\"", message.Attachments[index].FileName))
	this.Ctx.Output.Header("Content-Type", message.Attachments[index].ContentType)
	err = this.Ctx.Output.Body(message.Attachments[index].Content)
	check(err)
}

type ImageController struct {
	web.Controller
}

// getImage extracts an inline image from a mail.  It returns the image data,
// content type, and – if there is one – a filename.
func getImage(message *enmime.Envelope, cid string) ([]byte, string, string, error) {
	var allParts []*enmime.Part
	allParts = append(allParts, message.Inlines...)
	allParts = append(allParts, message.OtherParts...)
	for _, part := range allParts {
		if part.ContentID != "" {
			match := cidRegex.FindStringSubmatch(part.ContentID)
			if len(match) == 2 {
				partCid := match[1]
				if "cid:"+partCid == cid {
					return part.Content, part.ContentType, part.FileName, nil
				}
			} else {
				logger.Println("Could not match", part.ContentID)
			}
		}
	}
	return nil, "", "", fmt.Errorf("image %v not found in email", cid)
}

// Controller for downloading mail images.
func (this *ImageController) Get() {
	_, _, _, _, _, _, message, _ := getMailAndThreadRoot(&this.Controller)
	cid := this.Ctx.Input.Param(":cid")
	content, contentType, filename, err := getImage(message, cid)
	check(err)
	if filename == "" {
		this.Ctx.Output.Header("Content-Disposition", "inline")
	} else {
		this.Ctx.Output.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%v\"", filename))
	}
	this.Ctx.Output.Header("Content-Type", contentType)
	err = this.Ctx.Output.Body(content)
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
	_, _, _, hashID, _, _, _, _ := getMailAndThreadRoot(&this.Controller)
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

type MailRequestController struct {
	web.Controller
}

// Controller for requesting the hash of a certain email.
func (this *MailRequestController) Get() {
	loginName := getLogin(this.Ctx.Input.Header("Authorization"))
	emailAddress := getEmailAddress(loginName)
	if emailAddress == "" {
		logger.Panicf("email address of %v not found", loginName)
	}
	messageID := messageIDfromURL(this.Ctx.Input.Param(":messageid"))
	hashID := messageIDToHashID(messageID)
	mailPathsLock.RLock()
	mailPath := mailPaths[hashID]
	mailPathsLock.RUnlock()
	if mailPath == "" {
		this.Abort("404")
	}
	file, err := os.Open(mailPath)
	check(err)
	defer func() {
		err := file.Close()
		check(err)
	}()
	message, err := mail.ReadMessage(file)
	if err != nil {
		logger.Println(err)
		this.Abort("404")
	}
	matches := emailRegex.FindAllStringSubmatch(message.Header.Get("From"), -1)
	matches = append(matches, emailRegex.FindAllStringSubmatch(message.Header.Get("To"), -1)...)
	matches = append(matches, emailRegex.FindAllStringSubmatch(message.Header.Get("Cc"), -1)...)
	matches = append(matches, emailRegex.FindAllStringSubmatch(message.Header.Get("Bcc"), -1)...)
	addresses := make(map[string]bool)
	for _, match := range matches {
		addresses[strings.ToLower(match[0])] = true
	}
	var found bool
	for _, address := range permissions.Addresses[loginName] {
		if addresses[address] {
			found = true
			break
		}
	}
	if !found {
		this.Abort("403")
	}
	adminMails := permissions.Addresses[permissions.Admin]
	if len(adminMails) == 0 {
		this.Abort("500")
	}
	adminMail := adminMails[0]
	link := fmt.Sprintf("%v/%v", rootURL, hashID)
	fullThreadLink := fmt.Sprintf("%v?tokenFull=%v", link, string(hashMessageID(messageID, "full")))
	mailContent := new(bytes.Buffer)
	err = requestMailTemplate.Execute(mailContent, map[string]string{
		"loginName": loginName, "link": link, "fullThreadLink": fullThreadLink})
	check(err)
	err = enmime.Builder().
		From("", adminMail).
		Subject("Request for hash ID for mail "+loginName).
		ReplyTo("", emailAddress).
		Text(mailContent.Bytes()).
		To("", adminMail).Send("postfix:587", nil)
	check(err)
	this.TplName = "mailRequest.tpl"
	this.Data["messageid"] = messageID
}

type HealthController struct {
	web.Controller
}

// Controller for the /healthz endpoint.
func (this *HealthController) Get() {
	err := this.Ctx.Output.Body([]byte{})
	check(err)
}

func init() {
	templateContent, err := os.ReadFile("requestMail.tpl")
	check(err)
	requestMailTemplate = textTemplate.Must(textTemplate.New("mail").Parse(string(templateContent)))
}
