{{range .Children}}
<li>
  {{if .Link}}
  <a href="../{{.Link}}"><strong>{{.From}}:</strong> {{.Subject}}</a>
  {{else}}
  <strong>{{.From}}:</strong> {{.Subject}}
  {{end}}
</li>
<ul>{{template "thread.tpl" .}}</ul>
{{end}}
