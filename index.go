package main

import (
	"cloud.google.com/go/storage"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/cheggaaa/pb/v3"
	"github.com/mohamedamer/cloudio/io"
	"google.golang.org/api/iterator"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"
)

const BATCH_SIZE = 100
const FILES_TO_INDEX = 442019

type IndexService struct {
	fileIO      *io.GcsIO
	searchIndex *bleve.Index
}

type Document struct {
	docID   string
	jsonDoc interface{}
}

func initProd(searchIndex *bleve.Index, gcsClient *storage.Client) *IndexService {
	bucket := "plantdb-json"
	bucketHandle := gcsClient.Bucket(bucket)
	gcsIO := &io.GcsIO{BucketHandle: bucketHandle}
	return &IndexService{
		fileIO:      gcsIO,
		searchIndex: searchIndex,
	}
}

func main() {
	indexPath := "./search-indexes/plantdb.index"
	ctx := context.Background()
	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer gcsClient.Close()

	searchIndex, err := bleve.Open(indexPath)
	if err == bleve.ErrorIndexPathDoesNotExist {
		searchIndex, err = buildIndex(indexPath)
		if err != nil {
			log.Fatal(err)
		}
	} else if err != nil {
		log.Fatal(err)
	} else {
		log.Println("Opening existing index...")
	}

	indexService := initProd(&searchIndex, gcsClient)
	args := os.Args[1:]
	if args[0] == "server" {
		http.HandleFunc("/search", indexService.search)

		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
			log.Printf("Defaulting to port %s", port)
		}

		log.Printf("Listening on port %s", port)
		log.Printf("Open http://localhost:%s in the browser", port)
		log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), nil))
	} else if args[0] == "index" {
		fileNames := indexService.listObjects()
		documents := indexService.read(fileNames, ctx)
		indexService.index(documents)
	}
}

func (is *IndexService) search(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil {
		handleError(w, errors.New("empty search request"))
		return
	}

	requestBody, err := ioutil.ReadAll(r.Body)
	if err != nil {
		handleError(w, err)
		return
	}

	index := *is.searchIndex
	term := string(requestBody)
	queryString := fmt.Sprintf("%v", term)
	log.Printf("query term %v\n", queryString)
	query := bleve.NewMatchQuery(queryString)
	searchRequest := bleve.NewSearchRequest(query)
	searchRequest.Size = 52
	searchResult, err := index.Search(searchRequest)
	if err != nil {
		handleError(w, err)
		return
	}
	hits, err := json.Marshal(searchResult.Hits)
	if err != nil {
		handleError(w, err)
		return
	}
	w.Write(hits)
}

func (is *IndexService) listObjects() <-chan string {
	ctx := context.Background()
	objectsIterator := is.fileIO.BucketHandle.Objects(ctx, nil)
	filesToIndex := make(chan string)
	go func(c chan string) {
		for {
			attr, err := objectsIterator.Next()
			if err == iterator.Done {
				break
			} else if err != nil {
				log.Fatalf("while iterating GCS bucket %v", err)
			}
			c <- attr.Name
		}
		close(filesToIndex)
	}(filesToIndex)

	return filesToIndex
}

func (is *IndexService) read(filesToRead <-chan string,
	ctx context.Context) <-chan Document {
	filesToIndex := make(chan Document)
	sem := make(chan int, 100)
	go func(c <-chan string) {
		for fileName := range c {
			sem <- 1
			go func(d chan Document, docID string) {
				jsonBytes, err := is.fileIO.Read(docID, ctx)
				if err != nil {
					log.Fatalf("Couldn't read file for indexing %v with error%v", docID, err)
				}

				var jsonDoc interface{}
				err = json.Unmarshal(jsonBytes, &jsonDoc)
				if err != nil {
					log.Fatalf("Couldn't unmarshal json for indexing %v with error %v", docID, err)
				}
				d <- Document{
					docID:   docID,
					jsonDoc: jsonDoc,
				}
				<-sem
			}(filesToIndex, fileName)
		}
		time.Sleep(time.Second*2)
		close(filesToIndex)
		close(sem)
	}(filesToRead)
	return filesToIndex
}

func (is *IndexService) index(documents <-chan Document) {
	progressBar := pb.StartNew(FILES_TO_INDEX)
	indexingBatch := (*is.searchIndex).NewBatch()
	count := 0

	for document := range documents {
		err := indexingBatch.Index(document.docID, document.jsonDoc)
		if err != nil {
			log.Fatalf("Couldn't add indexing job to batch with error %v", err)
		}
		count++

		if count%BATCH_SIZE == 0 {
			err := (*is.searchIndex).Batch(indexingBatch)
			if err != nil {
				log.Fatalf("Couldn't run indexing batch with error %v", err)
				return
			}
			indexingBatch = (*is.searchIndex).NewBatch()

		}
		progressBar.Increment()
	}

	err := (*is.searchIndex).Batch(indexingBatch)
	if err != nil {
		log.Fatalf("Couldn't run last indexing batch with error %v", err)
		return
	}

	progressBar.Add(indexingBatch.Size())
	progressBar.Finish()
}

func buildIndex(path string) (bleve.Index, error) {
	textFieldMapping := bleve.NewTextFieldMapping()
	textFieldMapping.Analyzer = keyword.Name

	documentMapping := bleve.NewDocumentMapping()
	documentMapping.AddFieldMappingsAt("name", textFieldMapping)
	documentMapping.AddFieldMappingsAt("bloomSize", textFieldMapping)
	documentMapping.AddFieldMappingsAt("lifeCycle", textFieldMapping)
	documentMapping.AddFieldMappingsAt("SunRequirement", textFieldMapping)
	documentMapping.AddFieldMappingsAt("soilPHPreferences", textFieldMapping)
	documentMapping.AddFieldMappingsAt("wildlifeAttractant", textFieldMapping)

	indexMapping := bleve.NewIndexMapping()
	indexMapping.AddDocumentMapping("plantdb", documentMapping)

	indexMapping.DefaultAnalyzer = "en"

	index, err := bleve.New(path, indexMapping)
	if err != nil {
		return nil, err
	}

	return index, nil
}

func handleError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	log.Printf("ERROR: %s", err)
}
