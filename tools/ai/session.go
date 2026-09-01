package ai

import "sync"

var gameSession struct {
	sync.RWMutex
	id string
}

// SetGameSessionID stores the current game run ID supplied by the runner.
func SetGameSessionID(id string) {
	gameSession.Lock()
	gameSession.id = id
	gameSession.Unlock()
}

func currentGameSessionID() string {
	gameSession.RLock()
	defer gameSession.RUnlock()
	return gameSession.id
}
