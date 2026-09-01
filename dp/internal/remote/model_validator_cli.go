package remote

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"DP/internal/domain"
)

const modelValidatorProtocol = 1

type modelValidatorEvent struct {
	Type       string                  `json:"type"`
	Protocol   int                     `json:"protocol,omitempty"`
	BytesRead  int64                   `json:"bytes_read,omitempty"`
	TotalBytes int64                   `json:"total_bytes,omitempty"`
	Inspection *ModelArchiveInspection `json:"inspection,omitempty"`
	ErrorCode  string                  `json:"error_code,omitempty"`
	Message    string                  `json:"message,omitempty"`
}

// RunModelValidatorCLI runs the restricted model archive validator subcommand.
// It intentionally has no access to DP configuration, database, or SSH credentials.
func RunModelValidatorCLI(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("model-validator", flag.ContinueOnError)
	flags.SetOutput(stderr)
	archivePath := flags.String("archive", "", "model tar.gz path")
	maxExpanded := flags.Int64("max-expanded", 0, "maximum expanded bytes")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *archivePath == "" || *maxExpanded <= 0 {
		fmt.Fprintln(stderr, "--archive and a positive --max-expanded are required")
		return 2
	}
	file, err := os.Open(*archivePath)
	if err != nil {
		writeValidatorError(stdout, "MODEL_UPLOAD_REMOTE_MISSING", "无法打开远端模型包")
		return 1
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeValidatorError(stdout, "MODEL_ARCHIVE_INVALID", "模型包不是普通文件")
		return 1
	}

	encoder := json.NewEncoder(stdout)
	_ = encoder.Encode(modelValidatorEvent{Type: "hello", Protocol: modelValidatorProtocol, TotalBytes: info.Size()})
	reader := &validatorProgressReader{
		r: file, total: info.Size(), interval: time.Second,
		report: func(done, total int64) {
			_ = encoder.Encode(modelValidatorEvent{Type: "progress", BytesRead: done, TotalBytes: total})
		},
	}
	inspection, err := inspectModelArchive(reader, *maxExpanded)
	if err != nil {
		var appErr *domain.AppError
		if errors.As(err, &appErr) {
			writeValidatorError(stdout, appErr.Code, appErr.Message)
		} else {
			writeValidatorError(stdout, "MODEL_ARCHIVE_INVALID", err.Error())
		}
		return 1
	}
	reader.finish()
	_ = encoder.Encode(modelValidatorEvent{Type: "result", Inspection: &inspection})
	return 0
}

func writeValidatorError(output io.Writer, code, message string) {
	_ = json.NewEncoder(output).Encode(modelValidatorEvent{Type: "error", ErrorCode: code, Message: message})
}

type validatorProgressReader struct {
	r        io.Reader
	total    int64
	read     int64
	last     time.Time
	interval time.Duration
	report   func(int64, int64)
}

func (r *validatorProgressReader) Read(buffer []byte) (int, error) {
	n, err := r.r.Read(buffer)
	r.read += int64(n)
	now := time.Now()
	if r.report != nil && (r.last.IsZero() || now.Sub(r.last) >= r.interval || err == io.EOF) {
		r.last = now
		r.report(r.read, r.total)
	}
	return n, err
}

func (r *validatorProgressReader) finish() {
	if r.report != nil && r.read > 0 {
		r.report(r.read, r.total)
	}
}
