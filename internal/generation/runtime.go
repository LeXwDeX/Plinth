package generation

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type randomIDs struct{}

func (randomIDs) TaskID() TaskID       { return TaskID("task_" + randomToken(16)) }
func (randomIDs) AttemptID() AttemptID { return AttemptID("attempt_" + randomToken(16)) }
func (randomIDs) AssetID() AssetID     { return AssetID("asset_" + randomToken(16)) }
func (randomIDs) Token() string        { return randomToken(24) }

func randomToken(size int) string {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		panic(fmt.Sprintf("生成安全随机 ID 失败: %v", err))
	}
	return hex.EncodeToString(value)
}
