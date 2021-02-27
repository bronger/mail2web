package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/mail"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/beego/beego/v2/server/web"
	"github.com/jhillyerd/enmime"
	"golang.org/x/net/html"
)

func check(e error) {
	if e != nil {
		log.Panic(e)
	}
}

func getLogin(authHeader string) (login string) {
	components := strings.Split(authHeader, " ")
	if len(components) != 2 || components[0] != "Basic" {
		log.Panic("Invalid Authorization header " + authHeader)
	}
	rawField, err := base64.StdEncoding.DecodeString(components[1])
	check(err)
	field := string(rawField)
	components = strings.SplitN(field, ":", 2)
	if len(components) != 2 {
		log.Panic("Invalid Authorization login+password string: " + field)
	}
	return components[0]
}

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

func findThreadRoot(m *enmime.Envelope) (root string) {
	match := referenceRegex.FindStringSubmatch(m.GetHeader("Message-ID"))
	if len(match) < 2 {
		return ""
	}
	messageId := match[1]
	var stepBack func(string, int) (string, int)
	stepBack = func(messageId string, depth int) (root string, rootDepth int) {
		if depth > 100 {
			return
		}
		references := backReferences[messageId]
		if len(references) == 0 {
			return messageId, depth
		}
		for id, _ := range references {
			root_, rootDepth_ := stepBack(id, depth+1)
			if rootDepth_ > rootDepth {
				root, rootDepth = root_, rootDepth_
			}
		}
		return
	}
	root, _ = stepBack(messageId, 1)
	return root
}

func pathToLink(path_ string) string {
	prefix, id := path.Split(path_)
	_, folder := path.Split(strings.TrimSuffix(prefix, "/"))
	return folder + "/" + id
}

type threadNode struct {
	From, Subject string
	Link          string
	Children      []*threadNode
}

func threadNodeByMessageId(messageId string) *threadNode {
	mailPathsLock.RLock()
	path := mailPaths[messageId]
	mailPathsLock.RUnlock()
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	check(err)
	defer func() {
		err := file.Close()
		check(err)
	}()
	message, err := mail.ReadMessage(file)
	if err != nil {
		return nil
	}
	return &threadNode{
		message.Header.Get("From"),
		message.Header.Get("Subject"),
		pathToLink(path),
		make([]*threadNode, 0),
	}
}

func buildThread(root string) (rootNode *threadNode) {
	rootNode = threadNodeByMessageId(root)
	if rootNode == nil {
		return
	}
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
	return
}

func removeCurrentLink(link string, thread *threadNode) *threadNode {
	if thread.Link == link {
		thread.Link = ""
	}
	for _, child := range thread.Children {
		removeCurrentLink(link, child)
	}
	return thread
}

type MainController struct {
	web.Controller
}

func (this *MainController) Get() {
	login := getLogin(this.Ctx.Input.Header("Authorization"))
	log.Println(login)
	folder := this.Ctx.Input.Param(":folder")
	id := this.Ctx.Input.Param(":id")
	this.TplName = "index.tpl"
	this.Data["folder"] = folder
	this.Data["id"] = id
	link := folder + "/" + id
	file, err := os.Open(path.Join(mailDir, link))
	check(err)
	defer func() {
		err := file.Close()
		check(err)
	}()
	m, err := enmime.ReadEnvelope(file)
	check(err)
	this.Data["from"] = m.GetHeader("From")
	this.Data["subject"] = m.GetHeader("Subject")
	this.Data["to"] = m.GetHeader("To")
	this.Data["date"] = m.GetHeader("Date")
	this.Data["text"] = m.Text
	body, err := getBody(m.HTML)
	check(err)
	this.Data["html"] = template.HTML(body)
	attachments := make([]string, 0)
	for _, currentAttachment := range m.Attachments {
		attachments = append(attachments, currentAttachment.FileName)
	}
	this.Data["attachments"] = attachments
	threadRoot := findThreadRoot(m)
	if threadRoot != "" {
		this.Data["thread"] = removeCurrentLink(link, buildThread(threadRoot))
	}
}

type AttachmentController struct {
	web.Controller
}

func (this *AttachmentController) Get() {
	folder := this.Ctx.Input.Param(":folder")
	id := this.Ctx.Input.Param(":id")
	index, err := strconv.Atoi(this.Ctx.Input.Param(":index"))
	check(err)
	file, err := os.Open(path.Join(mailDir, folder, id))
	check(err)
	defer func() {
		err := file.Close()
		check(err)
	}()
	m, err := enmime.ReadEnvelope(file)
	check(err)
	this.Ctx.Output.Header("Content-Disposition",
		fmt.Sprintf("attachment; filename=\"%v\"", m.Attachments[index].FileName))
	this.Ctx.Output.Header("Content-Type", m.Attachments[index].ContentType)
	err = this.Ctx.Output.Body(m.Attachments[index].Content)
	check(err)
}

type HealthController struct {
	web.Controller
}

func (this *HealthController) Get() {
	this.Ctx.Output.Body([]byte{})
}
