package main

import (
	"bytes"
	"errors"
	"html/template"
	"io"
	"log"
	"os"
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

type MainController struct {
	web.Controller
}

func body(root *html.Node) (*html.Node, error) {
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

func renderNode(node *html.Node) string {
	var buffer bytes.Buffer
	writer := io.Writer(&buffer)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		html.Render(writer, child)
	}
	return buffer.String()
}

func (this *MainController) Get() {
	folder := this.Ctx.Input.Param(":folder")
	id := this.Ctx.Input.Param(":id")
	this.TplName = "index.tpl"
	this.Data["folder"] = folder
	this.Data["id"] = id
	file, err := os.Open("/home/bronger/Mail/" + folder + "/" + id)
	check(err)
	defer file.Close()
	m, err := enmime.ReadEnvelope(file)
	check(err)
	this.Data["from"] = m.GetHeader("From")
	this.Data["subject"] = m.GetHeader("Subject")
	this.Data["to"] = m.GetHeader("To")
	this.Data["date"] = m.GetHeader("Date")
	this.Data["text"] = m.Text
	html_document, err := html.Parse(strings.NewReader(m.HTML))
	check(err)
	body_node, err := body(html_document)
	check(err)
	body := renderNode(body_node)
	this.Data["html"] = template.HTML(body)
}
