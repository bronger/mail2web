{{range .Children}}
<li>
  {{if .OriginHashID}}
  <a href="{{.RootURL}}/{{.OriginHashID}}/{{.MessageID}}"><strong>{{.From}}:</strong> {{.Subject}}</a>
  {{else}}
  <strong>{{.From}}:</strong> {{.Subject}}
  {{end}}
</li>
<ul>{{template "thread.tpl" .}}</ul>
{{end}}
