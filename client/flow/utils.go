package flow

import (
  "fmt"
  "os"
)

func GetWorkerIdentity(tasklistName string) string {
  hostName, err := os.Hostname()
  if err != nil {
    hostName = "UnKnown"
  }
  return fmt.Sprintf("%d@%s@%s", os.Getpid(), hostName, tasklistName)
}