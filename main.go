package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fogleman/gg"
	"github.com/gorilla/handlers"
)

type Poster struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Full string `json:"full"`
	URL  string `json:"url"`
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	// Parse the multipart form
	err := r.ParseMultipartForm(10 << 20) // 10 MB limit
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Get the poster file from the form
	posterFile, _, err := r.FormFile("poster")
	if err != nil {
		http.Error(w, "Missing poster file", http.StatusBadRequest)
		return
	}
	defer posterFile.Close()

	// Save the poster file
	posterPath := filepath.Join("uploads", "poster.png")
	outFile, err := os.Create(posterPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer outFile.Close()
	_, err = io.Copy(outFile, posterFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the invites list file from the form
	invitesFile, _, err := r.FormFile("invites")
	if err != nil {
		http.Error(w, "Missing invites list file", http.StatusBadRequest)
		return
	}
	defer invitesFile.Close()

	// Save the invites list file
	invitesPath := filepath.Join("uploads", "invites.csv")
	outFile, err = os.Create(invitesPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer outFile.Close()
	_, err = io.Copy(outFile, invitesFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Process the files
	go processNames(posterPath, invitesPath)

	// Respond with success message
	response := map[string]string{"message": "Files uploaded and processing in background"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func processNames(posterPath, invitesPath string) {
	// Load the image file
	img, err := gg.LoadImage(posterPath)
	if err != nil {
		log.Printf("Error loading poster image: %v", err)
		return
	}

	// Open the CSV file
	file, err := os.Open(invitesPath)
	if err != nil {
		log.Printf("Error opening invites file: %v", err)
		return
	}
	defer file.Close()

	// Read the CSV file
	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		log.Printf("Error reading invites file: %v", err)
		return
	}

	// Create the output folder if it doesn't exist
	err = os.MkdirAll("guest_posters", os.ModePerm)
	if err != nil {
		log.Printf("Error creating output folder: %v", err)
		return
	}

	// Limit concurrency
	maxWorkers := 4 // Adjust this based on your system's capabilities
	semaphore := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup

	// Process each name
	for _, row := range records {
		wg.Add(1)
		semaphore <- struct{}{} // Acquire semaphore
		go func(name string) {
			defer func() {
				<-semaphore // Release semaphore
				wg.Done()
			}()

			addNameToPoster(img, name)
		}(row[0])
	}

	wg.Wait()
}

func addNameToPoster(img image.Image, name string) {
	// Create a new image context for each name
	dc := gg.NewContextForImage(img)

	// Set the font and color for the text
	if err := dc.LoadFontFace("Ananda.ttf", 70); err != nil {
		log.Printf("Error loading font: %v", err)
		return
	}
	dc.SetColor(color.White)

	// Add the text to the image
	dc.DrawStringAnchored(name, float64(dc.Width())/2, 640, 0.5, 0.5)

	// Save the modified image to a file
	outputPath := filepath.Join("guest_posters", fmt.Sprintf("Virginia & Alfred wedding invitation - %s.png", name))
	if err := dc.SavePNG(outputPath); err != nil {
		log.Printf("Error saving image for %s: %v", name, err)
		return
	}
}

func generatePosters(dir string) ([]Poster, error) {
	var posters []Poster
	id := 1

	// Walk through the directory
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Extract guest name from file name
		fileName := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))               // Remove file extension
		guestName := strings.TrimPrefix(fileName, "Virginia & Alfred wedding invitation - ") // Remove common prefix
		guestName = strings.TrimSpace(guestName)                                             // Trim any leading or trailing whitespace

		// Create a new poster
		poster := Poster{
			ID:   id,
			Name: guestName,
			Full: fileName,
			URL:  "http://localhost:8080/" + path, // URL to access the poster
		}
		posters = append(posters, poster)
		id++

		return nil
	})

	if err != nil {
		return nil, err
	}

	return posters, nil
}

func guestHandler(w http.ResponseWriter, r *http.Request) {
	postersDir := "guest_posters"
	posters, err := generatePosters(postersDir)
	if err != nil {
		log.Fatalf("Error generating posters: %v", err)
	}

	// Marshal posters to JSON and write to response
	if err := json.NewEncoder(w).Encode(posters); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func guestPosterHandler(w http.ResponseWriter, r *http.Request) {
	// Parse the file name from the request URL
	fileName := filepath.Base(r.URL.Path)
	filePath := filepath.Join("guest_posters", fileName)

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer file.Close()

	// Set the appropriate content type for your image
	w.Header().Set("Content-Type", "image/png")

	// Copy the file to the response writer
	_, err = io.Copy(w, file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func main() {
	// Create the uploads folder if it doesn't exist
	err := os.MkdirAll("uploads", os.ModePerm)
	if err != nil {
		log.Fatal(err)
	}

	// Specify the directory where guest posters are stored
	postersDir := "guest_posters"
	posters, err := generatePosters(postersDir)
	if err != nil {
		log.Fatalf("Error generating posters: %v", err)
	}

	// Create a CSV file to store the list of invites
	csvFilePath := filepath.Join("guest_posters", "wedding_guest_list.csv")
	csvFile, err := os.Create(csvFilePath)
	if err != nil {
		log.Fatalf("Error creating CSV file: %v", err)
	}
	defer csvFile.Close()

	// Write the headers to the CSV file
	csvWriter := csv.NewWriter(csvFile)
	csvWriter.Write([]string{"name", "full", "url"})

	// Write each poster to the CSV file
	for _, poster := range posters {
		err := csvWriter.Write([]string{poster.Name, poster.Full, poster.URL})
		if err != nil {
			log.Printf("Error writing poster to CSV: %v", err)
			continue
		}
	}
	csvWriter.Flush()

	fmt.Printf("Invites list saved to %s\n", csvFilePath)

	mux := http.NewServeMux()
	mux.HandleFunc("/upload", uploadHandler)
	mux.HandleFunc("/guests", guestHandler)
	mux.HandleFunc("/guest_posters/", guestPosterHandler)

	// Enable CORS globally
	allowedHeaders := handlers.AllowedHeaders([]string{"X-Requested-With", "Content-Type", "Authorization"})
	allowedMethods := handlers.AllowedMethods([]string{"GET", "POST", "PUT", "DELETE", "OPTIONS"})
	log.Fatal(http.ListenAndServe(":8080", handlers.CORS(allowedHeaders, allowedMethods)(mux)))
}
