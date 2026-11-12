package proberesults

import (
	"html/template"
	"net/http"
	"time"
)

var resultsPageTemplate = template.Must(template.New("results").Funcs(template.FuncMap{
	"duration": formatDuration,
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>probe-exporter results</title>
<style>
body { font-family: sans-serif; margin: 16px; }
table { border-collapse: collapse; width: 100%; }
th, td { border: 1px solid #999; padding: 4px 8px; text-align: left; vertical-align: top; }
pre { margin: 0; white-space: pre-wrap; }
.failed { color: #b00020; }
</style>
</head>
<body>
<h1>Latest results</h1>
<table>
<tr><th>Module</th><th>Target group</th><th>Target</th><th>Hostname</th><th>Query name</th><th>Result</th><th>Ended at</th><th>Duration</th><th>Log</th></tr>
{{- range .}}
<tr>
<td>{{.Identity.Module}}</td>
<td>{{.Identity.TargetGroup}}</td>
<td>{{.Identity.Target.Address}}</td>
<td>{{.Identity.Target.Hostname}}</td>
<td>{{.Identity.Target.QueryName}}</td>
<td>{{if .Success}}succeeded{{else}}<span class="failed">failed</span>{{end}}</td>
<td>{{.EndedAt.UTC.Format "2006-01-02T15:04:05Z"}}</td>
<td>{{duration .Duration}}</td>
<td><pre>{{.Log}}</pre></td>
</tr>
{{- end}}
</table>
</body>
</html>
`))

// ResultsPage serves every held result: its probe, whether it succeeded, when it ended, how long it took, and its log.
func (store *ResultStore) ResultsPage() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := resultsPageTemplate.Execute(writer, store.Results()); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
		}
	})
}

func formatDuration(duration time.Duration) string {
	return duration.Round(time.Millisecond).String()
}
