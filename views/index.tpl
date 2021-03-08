<!DOCTYPE html>
<html lang="en">
<head>
<title>Mail {{.hash}}</title>
</head>
<body>
<h1>Mail {{.hash}}</h1>

<p><a href="restricted/{{.hash}}/send">Send this to me!</a></p>
<p><a href="restricted/my_mails">Show me my mails</a></p>
{{if .thread}}
<h2>Thread</h2>
<ul>
  <li>
    {{if $.thread.HashId}}
    <a href="{{$.thread.HashId}}"><strong>{{$.thread.From}}:</strong> {{$.thread.Subject}}</a>
    {{else}}
    <strong>{{$.thread.From}}:</strong> {{$.thread.Subject}}
    {{end}}
  </li>
  <ul>
    {{template "thread.tpl" .thread}}
  </ul>
</ul>
{{end}}
<h2>Mail content</h2>
<table border="1">
  <tr>
    <th>From:</th>
    <td>{{.from}}</td>
  </tr>
  <tr>
    <th>Subject:</th>
    <td>{{.subject}}</td>
  </tr>
  <tr>
    <th>To:</th>
    <td>{{.to}}</td>
  </tr>
  <tr>
    <th>Date:</th>
    <td>{{.date}}</td>
  </tr>
</table>
<hr>
{{if .html}}
<div style="max-width: 40em">
{{.html}}
</div>
{{else}}
<pre style="white-space: pre-line">
{{.text}}
</pre>
{{end}}
<h2>Attachments</h2>
{{range $i, $name := .attachments}}
<p><a href="{{$.hash}}/{{$i}}">{{$name}}</a></p>
{{end}}
</body>
</html>
