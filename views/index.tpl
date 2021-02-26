<html>
<head>
<title>Mail {{.folder}}/{{.id}}</title>
</head>
<body>
<h1>Mail {{.folder}}/{{.id}}</h1>
{{if .thread}}
<h2>Thread</h2>
<ul>
  <li><a href="../{{$.thread.Link}}"><strong>{{$.thread.From}}:</strong> {{$.thread.Subject}}</a></li>
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
<pre>
{{.text}}
</pre>
{{end}}
<h2>Attachments</h2>
{{range $i, $name := .attachments}}
<p><a href="{{$.id}}/{{$i}}">{{$name}}</a></p>
{{end}}
</body>
</html>
