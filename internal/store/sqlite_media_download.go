package store

import (
	"encoding/json"

	"github.com/antoinebaudrimont-beep/walite/internal/model"
)

const mediaDownloadRefVersion = 1

type mediaDownloadRef struct {
	Version       int    `json:"version"`
	DirectPath    string `json:"direct_path"`
	MediaKey      []byte `json:"media_key"`
	FileSHA256    []byte `json:"file_sha256"`
	FileEncSHA256 []byte `json:"file_enc_sha256"`
}

func encodeMediaDownload(download model.MediaDownload) ([]byte, error) {
	data, err := json.Marshal(mediaDownloadRef{
		Version: mediaDownloadRefVersion, DirectPath: download.DirectPath(), MediaKey: download.MediaKey(),
		FileSHA256: download.FileSHA256(), FileEncSHA256: download.FileEncSHA256(),
	})
	if err != nil || len(data) > maxMediaDownloadRefBytes {
		return nil, newSQLiteError(ErrStoreRejected, err)
	}
	return data, nil
}

func decodeMediaDownload(kind model.MediaKind, name, mimeType string, declaredBytes uint64, data []byte) (model.Media, error) {
	var ref mediaDownloadRef
	if len(data) == 0 || len(data) > maxMediaDownloadRefBytes || json.Unmarshal(data, &ref) != nil || ref.Version != mediaDownloadRefVersion {
		return model.Media{}, newSQLiteError(ErrCorruptCache, nil)
	}
	return model.NewDownloadableMedia(kind, name, mimeType, ref.DirectPath, ref.MediaKey, ref.FileSHA256, ref.FileEncSHA256, declaredBytes)
}
