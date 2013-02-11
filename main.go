package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

var root string
var cache map[string]*Page
var tmpl [][]byte

func main() {
  parseOptions()

	var err error
	root, err = filepath.Abs(*optPath)

	if err != nil {
		log.Fatalf("Could not get the absolute path of %v. %v", *optPath, err)
	}

	cache = make(map[string]*Page)

	setupStatic()
	setupTemplate()

	http.HandleFunc("/", renderer)

	err = http.ListenAndServe(*optAddr, nil)

	if err != nil {
		log.Fatalf("Could not serve static files at path %v. %v", root, err)
	}
}

func setupStatic() {
  static := "/"+*optStaticDir+"/"
  public := filepath.Join(root, *optStaticDir)

	h := http.StripPrefix(static, http.FileServer(http.Dir(public)))

	http.Handle(static, h)
  http.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
    http.ServeFile(w, r, filepath.Join(public, "favicon.ico"))
  })
  http.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
    http.ServeFile(w, r, filepath.Join(public, "robots.txt"))
  })
}


func setupTemplate() {
	path := filepath.Join(root, *optTemplate)
	b, err := ioutil.ReadFile(path)

	if err != nil {
		log.Fatal("Could not read template: ", err)
	}

  tmpl = bytes.Split(b, []byte("{{content}}"))

	if len(tmpl) != 2 {
		log.Fatal("Template was not in a valid format")
	}
}

func renderer(w http.ResponseWriter, r *http.Request) {
	p, file := path.Split(r.URL.Path)

	if p == "" {
		p = *optHomeDir
	}

	if file == "" {
		file = *optDefault
	}

  abs := path.Join(root, p, file) + "." + *optExt

  var page *Page
  var ok bool
  var err error

  if page, ok = cache[abs]; !ok {
    page, err = GetPage(abs)

    if err != nil {
      log.Printf("[%v] %v", abs, err)
      writeError(w, r, err)
      return
    }

    if !*optDebug {
      cache[abs] = page
    }
  }

  write(w, r, page)
}

func write(w http.ResponseWriter, r *http.Request, page *Page) {
  if etag := r.Header.Get("If-None-Match"); strings.EqualFold(etag, page.Hash) {
    w.WriteHeader(http.StatusNotModified)
    return
  }

  w.Header().Set("Etag", page.Hash)
  w.Header().Set("Content-Type", "text/html; charset=UTF-8")

  if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
    b := new(bytes.Buffer)
    gz := gzip.NewWriter(b)
    gz.Write(tmpl[0])
    gz.Write(page.Content)
    gz.Write(tmpl[1])
    gz.Close()

    w.Header().Set("Content-Encoding", "gzip")
    w.Header().Set("Content-Length", strconv.Itoa(b.Len()))

    b.WriteTo(w)
  } else {
    w.Header().Set("Content-Length", strconv.Itoa(len(tmpl[0]) + len(page.Content) + len(tmpl[1])))
    w.Write(tmpl[0])
    w.Write(page.Content)
    w.Write(tmpl[1])
  }
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
  errFmt := "<h2>Oops! We've hit a bit of a problem...</h2><p>%v</p>"

  w.Header().Set("Content-Type", "text/html; charset=UTF-8")

  b := new(bytes.Buffer)
  b.Write(tmpl[0])

  if pErr, ok := err.(*PageError); ok {
    w.WriteHeader(pErr.StatusCode)
    fmt.Fprintf(b, errFmt, pErr.Message)
  } else {
    w.WriteHeader(http.StatusInternalServerError)
    fmt.Fprintf(b, errFmt, "Page not available")
  }

  b.Write(tmpl[1])
  b.WriteTo(w)
}
