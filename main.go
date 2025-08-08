package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/joho/godotenv"
	"google.golang.org/api/iterator"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	firebase "firebase.google.com/go"
	"firebase.google.com/go/db"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/option"
)

// AppVersion represents an app version in Firebase
type AppVersion struct {
	ID           string    `json:"id"`
	Version      string    `json:"version"`
	VersionCode  int       `json:"version_code"`
	DownloadURL  string    `json:"download_url"`
	ReleaseNotes string    `json:"release_notes"`
	FileSize     int64     `json:"file_size"`
	Checksum     string    `json:"checksum"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	StoragePath  string    `json:"storage_path"` // Path in Firebase Storage
}

type UpdateCheckRequest struct {
	CurrentVersion string `json:"current_version" binding:"required"`
	CurrentCode    int    `json:"current_code" binding:"required"`
	Platform       string `json:"platform" binding:"required"`
}

type UpdateCheckResponse struct {
	UpdateAvailable bool        `json:"update_available"`
	IsMandatory     bool        `json:"is_mandatory,omitempty"`
	LatestVersion   *AppVersion `json:"latest_version,omitempty"`
	ChangeLog       string      `json:"change_log,omitempty"`
}

var (
	firebaseDB    *db.Client
	storageClient *storage.Client
	ctx           = context.Background()
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: Could not load .env file (proceeding with system env vars)")
	}

	// Initialize Firebase
	initFirebase()

	// Initialize Gin router
	r := gin.Default()

	// Configure CORS
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization"}
	r.Use(cors.New(config))

	// OTA API routes
	api := r.Group("/api/v1/ota")
	{
		api.POST("/check-update", checkForUpdate)
		api.GET("/download/:version", downloadUpdate)
		api.POST("/upload", uploadUpdate)
		api.GET("/versions", getVersions)
		api.DELETE("/versions/:id", deleteVersion)
	}

	// Health check endpoint
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting Flutter OTA Update Server on port %s", port)
	log.Fatal(r.Run("0.0.0.0:" + port))
}

func initFirebase() {
	credsJSON := os.Getenv("FIREBASE_CREDENTIALS_JSON")
	if credsJSON == "" {
		log.Fatal("FIREBASE_CREDENTIALS_JSON environment variable not set")
	}

	projectID := os.Getenv("FIREBASE_PROJECT_ID")
	if projectID == "" {
		log.Fatal("FIREBASE_PROJECT_ID environment variable not set")
	}

	dbURL := os.Getenv("FIREBASE_DB_URL")
	bucketName := os.Getenv("FIREBASE_STORAGE_BUCKET")

	// 🔍 Log the config values
	log.Printf("Using Firebase project ID: %q", projectID)
	log.Printf("Using Firebase DB URL: %q", dbURL)
	log.Printf("Using Firebase storage bucket: %q", bucketName)

	conf := &firebase.Config{
		DatabaseURL:   dbURL,
		StorageBucket: bucketName,
	}

	// Handle credentials as JSON string (for Cloud Run) or file path (for local dev)
	var opt option.ClientOption
	if strings.HasPrefix(credsJSON, "{") {
		// It's a JSON string, use it directly
		log.Println("Using Firebase credentials from JSON string")
		opt = option.WithCredentialsJSON([]byte(credsJSON))
	} else {
		// It's a file path, use it as before
		log.Println("Using Firebase credentials from file path")
		opt = option.WithCredentialsFile(credsJSON)
	}

	app, err := firebase.NewApp(ctx, conf, opt)
	if err != nil {
		log.Fatalf("Failed to initialize Firebase app: %v", err)
	}

	firebaseDB, err = app.Database(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize Firebase DB client: %v", err)
	}

	storageClient, err = storage.NewClient(ctx, opt)
	if err != nil {
		log.Fatalf("Failed to initialize Storage client: %v", err)
	}

	log.Println("Successfully connected to Firebase services")

	// Optional: List buckets (already in your code)
	log.Println("Listing buckets...")
	it := storageClient.Buckets(ctx, projectID)
	for {
		bucketAttrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatalf("Error listing buckets: %v", err)
		}
		log.Println("Found bucket:", bucketAttrs.Name)
	}
}

func checkForUpdate(c *gin.Context) {
	var req UpdateCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Platform != "android" && req.Platform != "ios" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid platform"})
		return
	}

	ref := firebaseDB.NewRef("versions")
	var versions map[string]AppVersion
	if err := ref.Get(ctx, &versions); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	var latest *AppVersion
	for _, v := range versions {
		if !strings.HasPrefix(v.StoragePath, "releases/"+req.Platform+"/") {
			continue
		}
		if latest == nil || v.VersionCode > latest.VersionCode {
			temp := v // prevent referencing loop variable
			latest = &temp
		}
	}

	if latest == nil {
		c.JSON(http.StatusOK, UpdateCheckResponse{UpdateAvailable: false})
		return
	}

	updateAvailable := req.CurrentCode < latest.VersionCode

	response := UpdateCheckResponse{
		UpdateAvailable: updateAvailable,
		IsMandatory:     latest.VersionCode-req.CurrentCode >= 2,
		LatestVersion:   latest,
	}

	c.JSON(http.StatusOK, response)
}
func getVersions(c *gin.Context) {
	platform := c.Query("platform")

	ref := firebaseDB.NewRef("versions")
	var versions map[string]AppVersion
	log.Println("Fetching versions from Firebase...")
	if err := ref.OrderByChild("created_at").Get(ctx, &versions); err != nil {
		log.Printf("Firebase fetch error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch versions"})
		return
	}
	log.Println("Successfully fetched versions")

	// Convert map to slice and filter by platform if specified
	var versionsList []AppVersion
	for _, v := range versions {
		// If platform is specified, filter versions
		if platform != "" {
			isPlatformMatch := false
			if platform == "ios" && (v.StoragePath == "" || (len(v.StoragePath) >= 4 && v.StoragePath[0:4] == "ios_")) {
				isPlatformMatch = true
			} else if platform == "android" && (v.StoragePath == "" || (len(v.StoragePath) >= 8 && v.StoragePath[0:8] == "android_")) {
				isPlatformMatch = true
			}

			if !isPlatformMatch {
				continue // Skip this version if it doesn't match the platform
			}
		}

		// Add platform parameter to download URL if platform is specified
		if platform != "" {
			v.DownloadURL = fmt.Sprintf("%s?platform=%s", v.DownloadURL, platform)
		}

		versionsList = append(versionsList, v)
	}

	c.JSON(http.StatusOK, versionsList)
}

func downloadUpdate(c *gin.Context) {
	version := c.Param("version")
	platform := c.Query("platform")
	if platform == "" {
		platform = "android"
	}

	// Get all versions
	ref := firebaseDB.NewRef("versions")
	var versions map[string]AppVersion
	if err := ref.Get(ctx, &versions); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	var matched *AppVersion
	for _, v := range versions {
		if v.Version == version && strings.HasPrefix(v.StoragePath, "releases/"+platform+"/") {
			matched = &v
			break
		}
	}

	if matched == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Requested platform/version does not match any available file"})
		return
	}

	// Open from Firebase Storage
	bucketName := os.Getenv("FIREBASE_STORAGE_BUCKET")
	bucket := storageClient.Bucket(bucketName)
	obj := bucket.Object(matched.StoragePath)
	reader, err := obj.NewReader(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read file from storage"})
		return
	}
	defer reader.Close()

	// Content headers
	var fileExt, contentType string
	if platform == "ios" {
		fileExt = "ipa"
		contentType = "application/octet-stream"
	} else {
		fileExt = "apk"
		contentType = "application/vnd.android.package-archive"
	}

	fileName := fmt.Sprintf("app-v%s.%s", version, fileExt)
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	c.Header("Content-Type", contentType)
	c.Header("Content-Length", fmt.Sprintf("%d", matched.FileSize))

	_, copyErr := io.Copy(c.Writer, reader)
	if copyErr != nil {
		log.Printf("Error streaming file: %v", copyErr)
	}
}

func uploadUpdate(c *gin.Context) {
	// 1. Initialize context with timeout (10 minutes for large file uploads)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Minute)
	defer cancel()

	// 2. Parse and validate form data
	version := strings.TrimSpace(c.PostForm("version"))
	versionCodeStr := strings.TrimSpace(c.PostForm("version_code"))
	releaseNotes := strings.TrimSpace(c.PostForm("release_notes"))
	platform := strings.ToLower(strings.TrimSpace(c.PostForm("platform")))

	// Set default platform if not specified
	if platform == "" {
		platform = "android"
	}

	// Validate required fields
	if version == "" || versionCodeStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    "Missing required fields",
			"required": []string{"version", "version_code"},
		})
		return
	}

	// Validate version code
	versionCode, err := strconv.Atoi(versionCodeStr)
	if err != nil || versionCode <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    "Invalid version_code",
			"expected": "positive integer",
		})
		return
	}

	// 3. Check for existing versions
	ref := firebaseDB.NewRef("versions")

	// Check by version code
	query := ref.OrderByChild("version_code").EqualTo(versionCode).LimitToFirst(1)
	var existingVersions map[string]AppVersion
	if err := query.Get(ctx, &existingVersions); err != nil {
		log.Printf("Database query error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Could not check for existing versions",
		})
		return
	}

	if len(existingVersions) > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error": fmt.Sprintf("Version code %d already exists", versionCode),
		})
		return
	}

	// 4. Process file upload
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "No file uploaded",
		})
		return
	}

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(file.Filename))
	expectedExt := map[string]string{
		"ios":     ".ipa",
		"android": ".apk",
	}[platform]

	if ext != expectedExt {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":    fmt.Sprintf("Invalid file extension for %s platform", platform),
			"expected": expectedExt,
		})
		return
	}

	// 5. Open file stream
	src, err := file.Open()
	if err != nil {
		log.Printf("File open error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to process uploaded file",
		})
		return
	}
	defer src.Close()

	// 6. Initialize Firebase Storage
	bucketName := os.Getenv("FIREBASE_STORAGE_BUCKET")
	log.Printf("Using Firebase storage bucket: %q", bucketName)

	if bucketName == "" {
		log.Println("FIREBASE_STORAGE_BUCKET not configured")
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Server configuration error",
		})
		return
	}

	bucket := storageClient.Bucket(bucketName)
	if err != nil {
		log.Printf("Bucket initialization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to initialize storage",
		})
		return
	}

	// 7. Prepare storage path
	storagePath := fmt.Sprintf("releases/%s/%s-%d%s",
		platform,
		version,
		time.Now().Unix(),
		ext,
	)

	// 8. Upload to Firebase Storage with checksum calculation
	obj := bucket.Object(storagePath)
	w := obj.NewWriter(ctx)
	defer w.Close()

	hash := sha256.New()
	multiWriter := io.MultiWriter(w, hash)

	if _, err := io.Copy(multiWriter, src); err != nil {
		log.Printf("File upload error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to upload file",
		})
		return
	}

	if err := w.Close(); err != nil {
		log.Printf("Upload finalization error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to complete upload",
		})
		return
	}

	// 9. Set public read access (optional)
	if err := obj.ACL().Set(ctx, storage.AllUsers, storage.RoleReader); err != nil {
		log.Printf("Warning: Failed to set public access: %v", err)
	}

	// 10. Create version record in database
	newVersionRef, err := ref.Push(ctx, nil)
	if err != nil {
		log.Printf("Database reference creation error: %v", err)
		// Clean up uploaded file
		if err := obj.Delete(ctx); err != nil {
			log.Printf("Failed to clean up uploaded file: %v", err)
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to create version record",
		})
		return
	}

	// 11. Prepare version data
	appVersion := AppVersion{
		ID:           newVersionRef.Key,
		Version:      version,
		VersionCode:  versionCode,
		DownloadURL:  fmt.Sprintf("/api/v1/ota/download/%s?platform=%s", version, platform),
		ReleaseNotes: releaseNotes,
		FileSize:     file.Size,
		Checksum:     fmt.Sprintf("%x", hash.Sum(nil)),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		StoragePath:  storagePath,
	}

	// 12. Save to database
	if err := newVersionRef.Set(ctx, appVersion); err != nil {
		log.Printf("Database save error: %v", err)
		// Clean up uploaded file
		if err := obj.Delete(ctx); err != nil {
			log.Printf("Failed to clean up uploaded file: %v", err)
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to save version information",
		})
		return
	}

	// 13. Return success response
	c.JSON(http.StatusOK, gin.H{
		"message":      "Version uploaded successfully",
		"version":      appVersion,
		"download_url": appVersion.DownloadURL,
	})
}

func deleteVersion(c *gin.Context) {
	id := c.Param("id")

	// Get version info first
	ref := firebaseDB.NewRef("versions/" + id)
	var version AppVersion
	if err := ref.Get(ctx, &version); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	if version.Version == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Version not found"})
		return
	}

	// Delete from Firebase Storage
	bucketName := os.Getenv("FIREBASE_STORAGE_BUCKET")
	if bucketName == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Storage bucket not configured"})
		return
	}
	bucket := storageClient.Bucket(bucketName)

	if err := bucket.Object(version.StoragePath).Delete(ctx); err != nil {
		log.Printf("Warning: Failed to delete file from storage: %v", err)
	}

	// Delete from Firebase DB
	if err := ref.Delete(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete version"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Version deleted successfully"})
}

