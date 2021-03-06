package thumbnailer

// #cgo pkg-config: MagickWand
// #include <stdlib.h>
// #include "helper.h"
import "C"

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"github.com/robfig/revel"
	"gollery/app/common"
	"gollery/monitor"
	"gollery/utils"
	"io"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	THUMBNAILER_SIZE_PX             = 200
	THUMBNAILER_COMPRESSION_QUALITY = 75
	THUMBNAILER_CLEANUP_JOB_ID      = "@cleanup"
)

type ThumbnailSize int

const (
	THUMB_SMALL ThumbnailSize = 200
	THUMB_LARGE ThumbnailSize = 1600
)

type Thumbnailer struct {
	RootDir           string
	CacheDir          string
	thumbnailingQueue chan string
	queuedItems       map[string]bool
	queuedItemsMutex  sync.Mutex
	monitor           *monitor.Monitor
	monitorEvents     chan monitor.Event
}

func init() {
	C.gollery_thumbnailer_init()
}

func makeCacheKey(path string, size ThumbnailSize) string {
	h := sha1.New()
	io.WriteString(h, fmt.Sprintf("%s/%d", path, size))
	key := fmt.Sprintf("%.0x", h.Sum(nil))
	return key
}

func forAllThumbSizes(f func(s ThumbnailSize) bool) {
	for _, size := range []ThumbnailSize{THUMB_SMALL, THUMB_LARGE} {
		if !f(size) {
			break
		}
	}
}

// returns the list of thumb keys from this directory
func (t *Thumbnailer) checkCacheDir(dirPath string) ([]string, error) {
	dirFd, err := os.Open(dirPath)

	revel.INFO.Printf("Cleaning cache for directory '%s'", dirPath)

	if err != nil {
		return nil, utils.WrapError(err, "Cannot open directory '%s'", dirPath)
	}

	defer dirFd.Close()

	fis, err := dirFd.Readdir(-1)

	if err == io.EOF {
		return nil, nil
	}

	if err != nil {
		return nil, utils.WrapError(err, "Cannot read directory '%s'", dirPath)
	}

	allThumbKeys := []string{}

	for _, f := range fis {
		fPath := path.Join(dirPath, f.Name())

		if strings.HasPrefix(f.Name(), ".") {
			revel.TRACE.Printf("Skipping hidden file %s while checking thumbnails", fPath)
			continue
		}

		if f.IsDir() {
			childThumbKeys, err := t.checkCacheDir(fPath)

			if err != nil {
				revel.WARN.Printf("Cannot clean cache directory '%s': %s (skipping)", fPath, err)
			}

			allThumbKeys = append(allThumbKeys, childThumbKeys...)

			continue
		}

		fId := fPath[1+len(t.RootDir):]

		revel.TRACE.Printf("Checking thumbnail for %s", fId)

		forAllThumbSizes(func(size ThumbnailSize) bool {
			allThumbKeys = append(allThumbKeys, makeCacheKey(fId, size))

			hasThumbnail, err := t.HasThumbnail(fPath, size)

			if err != nil {
				revel.WARN.Printf("Error while checking thumbnail of file %s: %s (skipping)", fPath, err)
				return true
			}

			if !hasThumbnail {
				t.ScheduleThumbnail(fPath)
			}

			return true
		})
	}

	return allThumbKeys, nil
}

func (t *Thumbnailer) monitorEventsRoutine() {
	for x := range t.monitorEvents {
		if basename := path.Base(x.Path()); len(basename) > 0 && basename[0] == '.' {
			revel.TRACE.Printf("Skipping thumbnailer event for hidden file %s", x.Path())
			continue
		}

		if ev, ok := x.(*monitor.DeleteEvent); ok {
			if ev.IsDirectory {
				t.ScheduleThumbnail(THUMBNAILER_CLEANUP_JOB_ID)
				continue
			}

			t.DeleteThumbnail(ev.Path())
			continue
		}

		if ev, ok := x.(*monitor.CreateEvent); ok {
			if ev.Info.Mode().IsRegular() {
				t.ScheduleThumbnail(ev.Path())
			} else if ev.Info.IsDir() {
				err := t.monitor.Watch(ev.Path())

				if err != nil {
					revel.ERROR.Printf("Cannot setup a file monitor on %s: %s", ev.Path(), err)
					continue
				}

				_, err = t.checkCacheDir(ev.Path())

				if err != nil {
					revel.ERROR.Printf("Cannot create thumbnails for directory '%s': %s", ev.Path(), err)
				}
			}
			continue
		}
	}
}

func (t *Thumbnailer) thumbnailQueueRoutine() {
	thumbnailer := C.gollery_thumbnailer_new()
	defer C.gollery_thumbnailer_free(thumbnailer)

	for filePath := range t.thumbnailingQueue {
		t.queuedItemsMutex.Lock()
		delete(t.queuedItems, filePath)
		t.queuedItemsMutex.Unlock()

		if filePath == THUMBNAILER_CLEANUP_JOB_ID {
			t.CheckCache()
			continue
		}

		err := t.createThumbnail(thumbnailer, filePath)

		if err != nil {
			revel.ERROR.Printf("Couldn't create thumbnail for file '%s': %s", filePath, err)
		}

		revel.INFO.Printf("The thumbnailing queue now has %d items", len(t.thumbnailingQueue))
	}
}

func NewThumbnailer(rootDir string, cacheDir string, mon *monitor.Monitor) (*Thumbnailer, error) {
	t := &Thumbnailer{
		RootDir:           rootDir,
		CacheDir:          cacheDir,
		thumbnailingQueue: make(chan string, 256),
		queuedItems:       make(map[string]bool, 256),
		monitor:           mon,
		monitorEvents:     make(chan monitor.Event, 256),
	}

	revel.INFO.Printf("Starting %d thumbnailer routines", runtime.NumCPU())

	for i := 0; i < runtime.NumCPU(); i++ {
		go t.thumbnailQueueRoutine()
	}

	mon.Listen(t.monitorEvents)

	go t.monitorEventsRoutine()

	return t, nil
}

// Creates missing thumbnails
func (t *Thumbnailer) CheckCache() error {
	revel.INFO.Printf("Starting cache cleanup")

	allThumbKeys, err := t.checkCacheDir(t.RootDir)

	if err != nil {
		return utils.WrapError(err, "Cannot check thumbnail cache")
	}

	keyHash := make(map[string]bool, len(allThumbKeys))

	for _, key := range allThumbKeys {
		keyHash[key] = true
	}

	fd, err := os.Open(t.CacheDir)

	if err != nil {
		return utils.WrapError(err, "Cannot check thumbnail cache for stall thumbnails")
	}

	defer fd.Close()

	for {
		// Don't read all files at all, it might be a lot
		fis, err := fd.Readdir(1024)

		if err == io.EOF {
			break
		}

		if err != nil {
			return utils.WrapError(err, "Cannot list thumbnails while cleaning cache")
		}

		for _, fi := range fis {
			// Thumbnail corresponds to a known picture, leave it alone
			if _, exists := keyHash[fi.Name()]; exists {
				continue
			}

			thumbPath := path.Join(t.CacheDir, fi.Name())

			revel.INFO.Printf("Removing stale thumbnail with key %s", fi.Name())
			err = os.Remove(thumbPath)

			if err != nil && !os.IsNotExist(err) {
				return utils.WrapError(err, "Cannot delete thumbnail '%s' while cleaning up cache", thumbPath)
			}
		}
	}

	return nil
}

func (t *Thumbnailer) ScheduleThumbnail(filePath string) {
	revel.INFO.Printf("Scheduling thumbnailing of file %s", filePath)

	t.queuedItemsMutex.Lock()
	defer t.queuedItemsMutex.Unlock()

	if _, alreadyQueued := t.queuedItems[filePath]; alreadyQueued {
		revel.INFO.Printf("Thumbnailing already scheduled for file %s", filePath)
		return
	}

	t.thumbnailingQueue <- filePath
	t.queuedItems[filePath] = true
}

func (t *Thumbnailer) resizeImage(thumbnailer *C.GolleryThumbnailer, src string, dst string, size int) error {
	cSrc := C.CString(src)
	defer C.free(unsafe.Pointer(cSrc))

	cDst := C.CString(dst)
	defer C.free(unsafe.Pointer(cDst))

	var cError *C.char = nil
	defer C.free(unsafe.Pointer(cError))

	ret := C.gollery_thumbnailer_resize(thumbnailer, cSrc, cDst, C.size_t(size), &cError)

	if int(ret) != 1 {
		return errors.New(C.GoString(cError))
	}

	return nil
}

func (t *Thumbnailer) createThumbnail(thumbnailer *C.GolleryThumbnailer, filePath string) error {
	normalizedPath, err := common.NormalizePath(filePath)

	if err != nil {
		return utils.WrapError(err, "Invalid path '%s'", filePath)
	}

	if path.Dir(normalizedPath) == t.RootDir {
		revel.INFO.Printf("Not thumbnailing file in root directory: %s", normalizedPath)
	}

	fileId := normalizedPath[1+len(t.RootDir):]

	startTime := time.Now()

	forAllThumbSizes(func(size ThumbnailSize) bool {
		thumbKey := makeCacheKey(fileId, size)
		thumbPath := path.Join(t.CacheDir, thumbKey)

		err = t.resizeImage(thumbnailer, normalizedPath, thumbPath, int(size))

		return (err == nil)
	})

	if err != nil {
		return err
	}

	revel.INFO.Printf("Thumbnailed image '%s' in %.2f seconds", normalizedPath, time.Now().Sub(startTime).Seconds())

	return nil
}

func (t *Thumbnailer) DeleteThumbnail(filePath string) error {
	normalizedPath, err := common.NormalizePath(filePath)

	if err != nil {
		return utils.WrapError(err, "Invalid path '%s'", normalizedPath)
	}

	forAllThumbSizes(func(size ThumbnailSize) bool {
		fileId := normalizedPath[1+len(t.RootDir):]
		thumbKey := makeCacheKey(fileId, size)
		thumbPath := path.Join(t.CacheDir, thumbKey)

		err = os.Remove(thumbPath)

		if os.IsNotExist(err) {
			return true
		}

		if err != nil {
			err = utils.WrapError(err, "Cannot remove thumbnail")
			return false
		}

		return true
	})

	if err != nil {
		return err
	}

	revel.INFO.Printf("Deleted thumbnail for image '%s'", normalizedPath)

	return nil
}

func (t *Thumbnailer) ThumbnailQueueSize() int {
	return len(t.queuedItems)
}

func (t *Thumbnailer) HasThumbnail(filePath string, size ThumbnailSize) (bool, error) {
	normalizedPath, err := common.NormalizePath(filePath)

	if err != nil {
		return false, utils.WrapError(err, "Cannot normalize path")
	}

	fileId := normalizedPath[1+len(t.RootDir):]
	thumbKey := makeCacheKey(fileId, size)
	thumbPath := path.Join(t.CacheDir, thumbKey)

	_, err = os.Stat(thumbPath)

	if os.IsNotExist(err) {
		return false, nil
	}

	if err != nil {
		return false, utils.WrapError(err, "Cannot check if thumbnail exists")
	}

	return true, nil
}

func (t *Thumbnailer) GetThumbnail(filePath string, size ThumbnailSize) (*os.File, error) {
	normalizedPath, err := common.NormalizePath(filePath)

	if err != nil {
		return nil, utils.WrapError(err, "Cannot normalize path")
	}

	if normalizedPath == t.RootDir {
		return nil, fmt.Errorf("Invalid path")
	}

	fileId := normalizedPath[1+len(t.RootDir):]
	thumbKey := makeCacheKey(fileId, size)
	thumbPath := path.Join(t.CacheDir, thumbKey)

	fd, err := os.Open(thumbPath)

	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, utils.WrapError(err, "Cannot open thumbnail")
	}

	return fd, nil
}
