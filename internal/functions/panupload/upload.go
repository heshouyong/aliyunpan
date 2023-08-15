// Copyright (c) 2020 tickstep.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package panupload

import (
	"context"
	"encoding/xml"
	"github.com/tickstep/library-go/logger"
	"github.com/tickstep/library-go/requester"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/tickstep/aliyunpan-api/aliyunpan"
	"github.com/tickstep/aliyunpan-api/aliyunpan/apierror"
	"github.com/tickstep/aliyunpan/internal/file/uploader"
	"github.com/tickstep/library-go/requester/rio"
)

type (
	PanUpload struct {
		panClient  *aliyunpan.PanClient
		targetPath string
		driveId    string

		// 网盘上传参数
		uploadOpEntity *aliyunpan.CreateFileUploadResult
		useInternalUrl bool
	}

	EmptyReaderLen64 struct {
	}
)

func (e EmptyReaderLen64) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

func (e EmptyReaderLen64) Len() int64 {
	return 0
}

func NewPanUpload(panClient *aliyunpan.PanClient, targetPath, driveId string, uploadOpEntity *aliyunpan.CreateFileUploadResult, useInternalUrl bool) uploader.MultiUpload {
	return &PanUpload{
		panClient:      panClient,
		targetPath:     targetPath,
		driveId:        driveId,
		uploadOpEntity: uploadOpEntity,
		useInternalUrl: useInternalUrl,
	}
}

func (pu *PanUpload) lazyInit() {
	if pu.panClient == nil {
		pu.panClient = &aliyunpan.PanClient{}
	}
}

func (pu *PanUpload) Precreate() (err error) {
	return nil
}

func (pu *PanUpload) UploadFile(ctx context.Context, partseq int, partOffset int64, partEnd int64, r rio.ReaderLen64, uploadClient *requester.HTTPClient) (uploadDone bool, uperr error) {
	pu.lazyInit()

	// check url expired or not
	uploadUrl := pu.uploadOpEntity.PartInfoList[partseq].UploadURL
	if pu.useInternalUrl {
		uploadUrl = pu.uploadOpEntity.PartInfoList[partseq].InternalUploadURL
	}
	if IsUrlExpired(uploadUrl) {
		// get renew upload url
		infoList := make([]aliyunpan.FileUploadPartInfoParam, 0)
		for _, item := range pu.uploadOpEntity.PartInfoList {
			infoList = append(infoList, aliyunpan.FileUploadPartInfoParam{
				PartNumber: item.PartNumber,
			})
		}
		refreshUploadParam := &aliyunpan.GetUploadUrlParam{
			DriveId:      pu.uploadOpEntity.DriveId,
			FileId:       pu.uploadOpEntity.FileId,
			PartInfoList: infoList,
			UploadId:     pu.uploadOpEntity.UploadId,
		}
		newUploadInfo, err := pu.panClient.GetUploadUrl(refreshUploadParam)
		if err != nil {
			logger.Verboseln(err)
			return false, &uploader.MultiError{
				Err:        uploader.UploadUrlExpired,
				Terminated: false,
			}
		}
		pu.uploadOpEntity.PartInfoList = newUploadInfo.PartInfoList
	}

	var respErr *uploader.MultiError
	uploadFunc := func(httpMethod, fullUrl string, headers map[string]string) (*http.Response, error) {
		var resp *http.Response
		var respError error = nil
		respErr = nil
		var err error

		// do http upload request
		if uploadClient == nil {
			uploadClient = requester.NewHTTPClient()
			uploadClient.SetTimeout(0)
			uploadClient.SetKeepAlive(true)
		}
		resp, err = uploadClient.Req(httpMethod, fullUrl, r, headers)
		if err != nil {
			logger.Verbosef("分片上传出错: 分片%d => %s\n", partseq, err)
		}

		if resp != nil {
			if blen, e := strconv.Atoi(resp.Header.Get("content-length")); e == nil {
				if blen > 0 {
					buf := make([]byte, blen)
					resp.Body.Read(buf)
					logger.Verbosef("分片上传出错: 分片%d => %s\n", partseq, string(buf))

					errResp := &apierror.ErrorXmlResp{}
					if err := xml.Unmarshal(buf, errResp); err == nil {
						if errResp.Code != "" {
							if "PartNotSequential" == errResp.Code || "NoSuchUpload" == errResp.Code {
								respError = uploader.UploadPartNotSeq
								respErr = &uploader.MultiError{
									Err:           uploader.UploadPartNotSeq,
									Terminated:    false,
									NeedStartOver: true,
								}
								return resp, respError
							} else if "AccessDenied" == errResp.Code && "Request has expired." == errResp.Message {
								respError = uploader.UploadUrlExpired
								respErr = &uploader.MultiError{
									Err:        uploader.UploadUrlExpired,
									Terminated: false,
								}
								return resp, respError
							} else if "PartAlreadyExist" == errResp.Code {
								respError = uploader.UploadPartAlreadyExist
								respErr = &uploader.MultiError{
									Err:        uploader.UploadPartAlreadyExist,
									Terminated: false,
								}
								return resp, respError
							}
						}
					}
				}
			} else {
				logger.Verbosef("分片上传出错: %d分片 => 原因未知\n", partseq)
			}

			// 不可恢复的错误
			switch resp.StatusCode {
			case 400, 401, 403, 413, 600:
				respError = uploader.UploadTerminate
				respErr = &uploader.MultiError{
					Terminated: true,
				}
			}
		} else {
			respError = uploader.UploadTerminate
			respErr = &uploader.MultiError{
				Terminated: true,
			}
		}
		return resp, respError
	}

	// 上传一个分片数据
	uploadUrl = pu.uploadOpEntity.PartInfoList[partseq].UploadURL
	if pu.useInternalUrl {
		uploadUrl = pu.uploadOpEntity.PartInfoList[partseq].InternalUploadURL
	}
	apiError := pu.panClient.UploadFileData(uploadUrl, uploadFunc)

	if respErr != nil {
		if respErr.Err == uploader.UploadUrlExpired {
			// URL过期，获取新的URL
			guur, er := pu.panClient.GetUploadUrl(&aliyunpan.GetUploadUrlParam{
				DriveId:      pu.driveId,
				FileId:       pu.uploadOpEntity.FileId,
				UploadId:     pu.uploadOpEntity.UploadId,
				PartInfoList: []aliyunpan.FileUploadPartInfoParam{{PartNumber: (partseq + 1)}}, // 阿里云盘partNum从1开始计数，partSeq从0开始
			})
			if er != nil {
				return false, &uploader.MultiError{
					Terminated: false,
				}
			}

			// 获取新的上传URL重试一次
			pu.uploadOpEntity.PartInfoList[partseq] = guur.PartInfoList[0]
			uploadUrl := pu.uploadOpEntity.PartInfoList[partseq].UploadURL
			if pu.useInternalUrl {
				uploadUrl = pu.uploadOpEntity.PartInfoList[partseq].InternalUploadURL
			}
			apiError = pu.panClient.UploadFileData(uploadUrl, uploadFunc)
		} else if respErr.Err == uploader.UploadPartAlreadyExist {
			// already upload
			// success
			return true, nil
		} else if respErr.Err == uploader.UploadPartNotSeq {
			// 上传分片乱序了，需要重新从0分片开始上传
			// 先直接返回，后续再优化
			return false, respErr
		} else {
			return false, respErr
		}
	}

	if apiError != nil {
		return false, apiError
	}

	return true, nil
}

func (pu *PanUpload) CommitFile() (cerr error) {
	pu.lazyInit()
	var er *apierror.ApiError

	_, er = pu.panClient.CompleteUploadFile(&aliyunpan.CompleteUploadFileParam{
		DriveId:  pu.driveId,
		FileId:   pu.uploadOpEntity.FileId,
		UploadId: pu.uploadOpEntity.UploadId,
	})
	if er != nil && er.Code == apierror.ApiCodeDeviceSessionSignatureInvalid {
		_, e := pu.panClient.CreateSession(nil)
		if e == nil {
			// retry
			_, er = pu.panClient.CompleteUploadFile(&aliyunpan.CompleteUploadFileParam{
				DriveId:  pu.driveId,
				FileId:   pu.uploadOpEntity.FileId,
				UploadId: pu.uploadOpEntity.UploadId,
			})
		} else {
			logger.Verboseln("CreateSession failed")
		}
	}
	if er != nil {
		return er
	}

	// 视频文件触发云端转码请求
	pu.triggerVideoTranscodeAction()

	return nil
}

// TriggerVideoTranscodeAction 触发视频文件转码成功
func (pu *PanUpload) triggerVideoTranscodeAction() {
	// 视频文件触发云端转码请求
	if pu.uploadOpEntity != nil && IsVideoFile(pu.uploadOpEntity.FileName) {
		time.Sleep(3 * time.Second)
		_, er1 := pu.panClient.VideoGetPreviewPlayInfo(&aliyunpan.VideoGetPreviewPlayInfoParam{
			DriveId: pu.driveId,
			FileId:  pu.uploadOpEntity.FileId,
		})
		if er1 == nil {
			logger.Verboseln("触发视频文件转码成功：" + pu.uploadOpEntity.FileName)
		}
	}
}
