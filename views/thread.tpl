{{range .Children}}
<li><a href="../{{.Link}}"><strong>{{.From}}:</strong> {{.Subject}}</a></li>
<ul>{{template "thread.tpl" .}}</ul>
{{end}}
