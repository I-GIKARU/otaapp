# OTA Server - Go Backend

The Go backend for handling OTA updates for Flutter applications, built with the Gin framework and Firebase integrations.

## 🚀 Live Deployment

**Service URL**: https://ota-server-507772707301.us-central1.run.app

## 📋 Features

- **Version Check**: Verify available updates for Flutter apps
- **File Upload**: Secure and validate uploads of APK/IPA files
- **Firebase Integration**: Real-time Database and Cloud Storage
- **Cross-Platform Support**: Handles both Android (APK) and iOS (IPA) files
- **Checksum Validation**: SHA256 file integrity verification
- **CORS Support**: Cross-origin requests for web dashboard
- **Health Monitoring**: Built-in health check endpoint
- **Auto-scaling**: Cloud Run deployment with configurable scaling

## 🛠 Tech Stack

- **Backend**: Go
- **Framework**: Gin
- **Database**: Firebase Realtime Database
- **Storage**: Firebase Cloud Storage

## 📝 Key Files and Configuration

- **`main.go`**: Main application with all endpoints and Firebase integration
- **`Dockerfile`**: Multi-stage Docker build configuration
- **Firebase Credentials**: Loaded securely via Cloud Run secrets
- **`go.mod` & `go.sum`**: Go module dependencies

## 📊 Implementation Details

### Firebase Credentials Handling

The application supports both local development and Cloud Run deployment:

```go
// Auto-detects JSON string (Cloud Run) vs file path (local)
if strings.HasPrefix(credsJSON, "{") {
    // Cloud Run: JSON string from secrets
    opt = option.WithCredentialsJSON([]byte(credsJSON))
} else {
    // Local: File path
    opt = option.WithCredentialsFile(credsJSON)
}
```

### Data Structure

```go
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
    StoragePath  string    `json:"storage_path"`
}
```

### Security Features

- **File validation**: Extension and MIME type checking
- **Version conflict prevention**: Duplicate version code detection
- **Checksum verification**: SHA256 hash calculation
- **CORS configuration**: Secure cross-origin requests
- **Input sanitization**: Request validation and error handling

## 🚀 Deployment

### Cloud Run Deployment

Deployed as a containerized service on Google Cloud Run:

```bash
# Deploy to Cloud Run
gcloud run deploy ota-server \
  --image gcr.io/appupdate-3224f/ota-server \
  --platform managed \
  --region us-central1 \
  --allow-unauthenticated \
  --set-env-vars="FIREBASE_DB_URL=https://appupdate-3224f-default-rtdb.firebaseio.com" \
  --set-env-vars="FIREBASE_STORAGE_BUCKET=appupdate-3224f.firebasestorage.app" \
  --update-secrets="FIREBASE_CREDENTIALS_JSON=firebase-credentials:latest"
```

### Environment Variables

- **`FIREBASE_DB_URL`**: Your Firebase Realtime Database URL
- **`FIREBASE_STORAGE_BUCKET`**: Your Firebase Storage Bucket name

## 📦 Files Used for Deployment

- **`Dockerfile`**: Multi-stage Docker build
- **Cloud Run Configuration**: Environment setup via Dockerfile and gcloud CLI

## 💻 Development

### Prerequisites

- Go 1.23+

### Local Development

```bash
# Run locally
go run main.go
```

### API Endpoints

#### Health Check
- **`GET /health`**: Health check endpoint
  - Response: `{"status": "ok"}`

#### Version Management
- **`GET /api/v1/versions?platform={android|ios}`**: Get available versions
  - Query params: `platform` (optional)
  - Response: Array of AppVersion objects

- **`POST /api/v1/upload`**: Upload new app version
  - Content-Type: `multipart/form-data`
  - Fields:
    - `file`: APK/IPA file
    - `version`: Version string (e.g., "1.0.0")
    - `version_code`: Integer version code
    - `platform`: "android" or "ios"
    - `release_notes`: Optional release notes
  - Response: Upload confirmation with version details

- **`DELETE /api/v1/versions/:id`**: Delete a version
  - Path param: `id` - Version ID
  - Response: Deletion confirmation

#### Update Check (for Flutter apps)
- **`POST /api/v1/check-update`**: Check for app updates
  - Body:
    ```json
    {
      "current_version": "1.0.0",
      "current_code": 1,
      "platform": "android"
    }
    ```
  - Response:
    ```json
    {
      "update_available": true,
      "is_mandatory": false,
      "latest_version": { /* AppVersion object */ }
    }
    ```

- **`GET /api/v1/download/:version?platform={platform}`**: Download app file
  - Path param: `version` - Version string
  - Query param: `platform` - Target platform
  - Response: Binary file download
