{{range .Children}}
<li>
  {{if .HashId}}
  <a href="{{.RootURL}}/{{.HashId}}"><strong>{{.From}}:</strong> {{.Subject}}</a>
  {{else}}
  <strong>{{.From}}:</strong> {{.Subject}}
  {{end}}
</li>
<ul>{{template "thread.tpl" .}}</ul>
{{end}}
