package views

import (
	"html/template"
	"net/http"
	"path/filepath"
)

var (
	LayoutDir string = "views/layouts/"
	LayoutExr string = ".gohtml"
)

func NewView(layout string, files ...string) *View {
	layouts := layoutFiles()
	files = append(files, layouts...)
	t, err := template.ParseFiles(files...)
	panicIfError(err)
	return &View{Template: t, Layout:layout, StatusCode: http.StatusOK}
}

func NewViewWithStatusCode(layout string, statusCode int, files ...string) *View {
	layouts := layoutFiles()
	files = append(files, layouts...)
	t, err := template.ParseFiles(files...)
	panicIfError(err)
	return &View{Template: t, StatusCode: statusCode, Layout:layout}
}

type View struct {
	Template *template.Template
	Layout string
	StatusCode int
}

func (v *View) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := v.Render(w, nil)
	panicIfError(err)
}

func (v *View) Render(w http.ResponseWriter, data interface{}) error {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(v.StatusCode)
	return v.Template.ExecuteTemplate(w, v.Layout, data)
}

func layoutFiles()[]string {
	files, err := filepath.Glob(LayoutDir + "*" + LayoutExr)
	panicIfError(err)

	return files
}

func panicIfError(err error) {
	if err != nil {
		panic(err)
	}
}