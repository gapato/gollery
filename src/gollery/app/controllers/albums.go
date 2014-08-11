package controllers

import (
	"github.com/abustany/goexiv"
	"github.com/robfig/revel"
	"gollery/app"
	"gollery/app/common"
	"gollery/thumbnailer"
	"gollery/utils"
	"math/rand"
	"net/http"
	"os"
	"path"
	"strings"
)

type Albums struct {
	*revel.Controller
}

type AlbumInfo struct {
	Name  string `json:"name"`
	Cover string `json:"cover"`
	HasSubdirs bool   `json:"hasSubdirs"`
}

type AlbumData struct {
	Name     string         `json:"name"`
	Pictures []*PictureInfo `json:"pictures"`
}

type PictureInfo struct {
	Path     string            `json:"path"`
	Metadata map[string]string `json:"metadata"`
}

var ExifKeys = []string{
	"Exif.Photo.ExifVersion",
	"Exif.Image.Make",
	"Exif.Image.Model",
	"Exif.Photo.DateTimeOriginal",
	"Exif.Photo.ExposureTime",
	"Exif.Photo.ShutterSpeedValue",
	"Exif.Photo.FNumber",
	"Exif.Photo.ApertureValue",
	"Exif.Photo.ExposureBiasValue",
	"Exif.Photo.Flash",
	"Exif.Photo.FocalLength",
	"Exif.Photo.FocalLengthIn35mmFilm",
	"Exif.Photo.SubjectDistance",
	"Exif.Photo.ISOSpeedRatings",
	"Exif.Photo.ExposureProgram",
	"Exif.Photo.MeteringMode",
	"Exif.Image.ImageWidth",
	"Exif.Photo.PixelXDimension",
	"Exif.Image.ImageLength",
	"Exif.Photo.PixelYDimension",
	"Exif.Image.Copyright",
	"Exif.Photo.UserComment",
	"Exif.GPSInfo.GPSAltitudeRef",
	"Exif.GPSInfo.GPSAltitude",
	"Exif.GPSInfo.GPSLatitudeRef",
	"Exif.GPSInfo.GPSLatitude",
	"Exif.GPSInfo.GPSLongitudeRef",
	"Exif.GPSInfo.GPSLongitude",
}

func (c *Albums) Index(name string) revel.Result {
	revel.INFO.Printf("Navigating album %s", name)
	watchedPaths := app.Monitor.WatchedDirectories()
	dirs := make([]*AlbumInfo, 0, len(watchedPaths))

	for _, dir := range watchedPaths {
		if dir == common.RootDir || !strings.HasPrefix(dir, common.RootDir) {
			continue
		}

		dir = dir[1+len(common.RootDir):]
		if dir == name || !strings.HasPrefix(dir, name) || strings.LastIndex(dir, "/") > len(name) {
			continue
		}

		cover, err := c.getAlbumCover(dir)

		if err != nil {
			revel.ERROR.Printf("Cannot lookup cover for album '%s': %s", dir, err)
		}

		super, err :=  c.hasSubdirs(dir)

		if err != nil {
			revel.ERROR.Printf("Cannot determine if album '%s' has subdirectories: %s", dir, err)
		}

		dirs = append(dirs, &AlbumInfo{
			Name:  dir,
			Cover: cover,
			HasSubdirs: super,
		})
	}

	c.Response.Status = http.StatusOK
	c.Response.ContentType = "application/json"
	return c.RenderJson(dirs)
}

func (c *Albums) getAlbumCover(name string) (string, error) {
	fis, err := c.listPictures(name, true)

	if len(fis) == 0 {
		return "", nil
	}

	if err != nil {
		return "", utils.WrapError(err, "Cannot list album '%s'", name)
	}

	for i := 0; i < 10; i++ {
		fi := fis[rand.Int31n(int32(len(fis)))]
		filePath := path.Join(name, fi)

		hasThumbnail, err := app.Thumbnailer.HasThumbnail(filePath, thumbnailer.THUMB_SMALL)

		if err != nil {
			return "", utils.WrapError(err, "Cannot check if thumbnail exists for '%s'", filePath)
		}

		if !hasThumbnail {
			continue
		}

		return filePath, nil
	}

	return "", nil
}

func getMetadata(filePath string) (map[string]string, error) {
	fd, err := goexiv.Open(filePath)

	if err != nil {
		return nil, utils.WrapError(err, "Cannot open file to read metadata")
	}

	err = fd.ReadMetadata()

	if err != nil {
		return nil, utils.WrapError(err, "Cannot read metadata")
	}

	data := fd.GetExifData()

	metadata := make(map[string]string, len(ExifKeys))

	for _, key := range ExifKeys {
		val, err := data.FindKey(key)

		if err != nil {
			return nil, utils.WrapError(err, "Invalid EXIF key requested")
		}

		if val == nil {
			continue
		}

		metadata[key] = val.String()
	}

	return metadata, nil
}

func (c *Albums) hasSubdirs(name string) (bool, error) {
	dirPath := path.Join(common.RootDir, name)
	dirFd, err := os.Open(dirPath)

	if os.IsNotExist(err) {
		return false, nil
	}

	if err != nil {
		return false, utils.WrapError(err, "Cannot open folder")
	}

	defer dirFd.Close()

	fis, err := dirFd.Readdir(-1)

	if err != nil {
		return false, utils.WrapError(err, "Cannot list folder")
	}

	for _, fi := range fis {
		if fi.IsDir() {
			return true, nil
		}
	}
	return false, nil
}

func (c *Albums) listPictures(name string, recurse bool) ([]string, error) {
	dirPath := path.Join(common.RootDir, name)
	dirFd, err := os.Open(dirPath)

	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, utils.WrapError(err, "Cannot open folder")
	}

	defer dirFd.Close()

	fis, err := dirFd.Readdir(-1)

	if err != nil {
		return nil, utils.WrapError(err, "Cannot list folder")
	}

	var filteredFis []string
	var subDirs  []string
	var ext string

	for _, fi := range fis {
		if fi.IsDir() {
			subDirs = append(subDirs, fi.Name())
			continue
		}
		ext = strings.ToLower(path.Ext(fi.Name()))
		for _, allowedExt := range common.ImageExts {
			if strings.EqualFold(ext, allowedExt) {
				filteredFis = append(filteredFis, fi.Name())
				break
			}
		}
	}

	if len(filteredFis) == 0 && recurse && len(subDirs) > 0 {
		for _, dirName := range subDirs {
			recImgs, err := c.listPictures(path.Join(name, dirName), true)
			if err == nil {
				for _, img := range recImgs {
					filteredFis = append(filteredFis, path.Join(dirName, img))
				}
			}
		}
	}

	return filteredFis, nil
}

func (c *Albums) Show(name string) revel.Result {
	revel.INFO.Printf("Loading album %s", name)

	fis, err := c.listPictures(name, false)

	if err != nil {
		c.Response.Status = http.StatusInternalServerError
		return c.RenderError(utils.WrapError(err, "Cannot read album"))
	}

	albumData := &AlbumData{
		Name:     name,
		Pictures: make([]*PictureInfo, 0, len(fis)),
	}

	dirPath := path.Join(common.RootDir, name)

	for _, info := range fis {
		filePath := path.Join(dirPath, info)

		hasThumbnail, err := app.Thumbnailer.HasThumbnail(filePath, thumbnailer.THUMB_SMALL)

		if err != nil {
			c.Response.Status = http.StatusInternalServerError
			c.RenderError(utils.WrapError(err, "Cannot check for thumbnail"))
		}

		if !hasThumbnail {
			continue
		}

		metadata, err := getMetadata(filePath)

		if err != nil {
			revel.ERROR.Printf("Cannot read metadata for file '%s': %s", filePath, err)
			metadata = map[string]string{}
		}

		picInfo := &PictureInfo{
			Path:     info,
			Metadata: metadata,
		}

		albumData.Pictures = append(albumData.Pictures, picInfo)
	}

	return c.RenderJson(albumData)
}

func (c *Albums) Download(size, name string) revel.Result {
	return c.Todo()
}

// vim: noexpandtab
