package syncdrive

import (
	"fmt"
	"github.com/tickstep/aliyunpan-api/aliyunpan"
	"github.com/tickstep/aliyunpan/internal/utils"
	"github.com/tickstep/library-go/logger"
	"os"
	"testing"
	"time"
)

func TestFileActionMgrStart(t *testing.T) {
	refreshToken := "1640cc2d4ea...cb4d6242161a7"
	webToken, err := aliyunpan.GetAccessTokenFromRefreshToken(refreshToken)
	if err != nil {
		fmt.Println("get acccess token error")
		return
	}

	// pan client
	panClient := aliyunpan.NewPanClient(*webToken, aliyunpan.AppLoginToken{}, aliyunpan.AppConfig{
		AppId:     "25dzX3vbYqktVxyX",
		DeviceId:  "E75459EXhOTkI5ZI6S3qDHA3",
		UserId:    "",
		Nonce:     0,
		PublicKey: "",
	}, aliyunpan.SessionConfig{
		DeviceName: "Chrome浏览器",
		ModelName:  "Windows网页版",
	})
	user, _ := panClient.GetUserInfo()
	task := &SyncTask{
		Id:              "5b2d7c10-e927-4e72-8f9d-5abb3bb04814",
		DriveId:         user.FileDriveId,
		LocalFolderPath: "D:\\smb\\feny\\goprojects\\dev\\NS游戏备份",
		PanFolderPath:   "/sync_drive",
		Mode:            "sync",
		LastSyncTime:    "2022-05-26 19:29:19",

		syncDbFolderPath: "D:\\smb\\feny\\goprojects\\dev\\sync_drive",
		panClient:        panClient,
		syncOption: SyncOption{
			FileDownloadParallel:  2,
			FileUploadParallel:    2,
			FileDownloadBlockSize: int64(256 * 1024),
			FileUploadBlockSize:   aliyunpan.DefaultChunkSize,
			UseInternalUrl:        false,
			MaxDownloadRate:       0,
			MaxUploadRate:         0,
		},
	}
	task.setupDb()

	ft := NewFileActionTaskManager(task)
	ft.Start()

	//go func() {
	//	time.Sleep(10 * time.Second)
	//	task.Stop()
	//}()
	time.Sleep(50 * time.Minute)
	ft.Stop()
}

func TestFileTime(t *testing.T) {
	ts := utils.ParseTimeStr("2013-01-11 12:15:07")
	//ts = time.Now()
	if err := os.Chtimes("/Volumes/Downloads/dev/upload/password.key", ts, ts); err != nil {
		logger.Verbosef(err.Error())
	}
}

func TestLocalFileTime(t *testing.T) {
	if file, er := os.Stat("/Volumes/Downloads/dev/upload/password.key"); er == nil {
		fmt.Println(file.ModTime().Format("2006-01-02 15:04:05"))
	}
}
