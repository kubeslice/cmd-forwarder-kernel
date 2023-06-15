package fs

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"runtime"
	"strconv"
	"syscall"
	"unicode"

	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"

	"github.com/sirupsen/logrus"
)

func isDigits(s string) bool {
	for _, c := range s {
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

// GetInode returns Inode for file
func GetInode(file string) (uint64, error) {
	fileinfo, err := os.Stat(file)
	if err != nil {
		return 0, errors.Wrap(err, "error stat file")
	}
	stat, ok := fileinfo.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("not a stat_t")
	}
	return stat.Ino, nil
}

// ResolvePodNsByInode Traverse /proc/<pid>/<suffix> files,
// compare their inodes with inode parameter and returns file if inode matches
func ResolvePodNsByInode(inode uint64) (string, error) {
	files, err := ioutil.ReadDir("/proc")
	if err != nil {
		return "", errors.Wrap(err, "can't read /proc directory")
	}

	for _, f := range files {
		name := f.Name()
		if isDigits(name) {
			filename := path.Join("/proc", name, "/ns/net")
			tryInode, err := GetInode(filename)
			if err != nil {
				// Just report into log, do not exit
				logrus.Errorf("Can't find %s Error: %v", filename, err)
				continue
			}
			if tryInode == inode {
				cmdFound, _ := GetCmdline(name)
				logrus.Infof("Found a pod attached to the inode: %v, filename: %v", cmdFound, filename)
				return filename, nil
			}
		}
	}

	return "", errors.New("not found")
}

func filenameToURL(filename string) (u *url.URL, err error) {
	fi, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}
	u = &url.URL{
		Scheme: "inode",
		Host:   fmt.Sprintf("%d", fi.Sys().(*syscall.Stat_t).Dev),
		Path:   fmt.Sprintf("%d", fi.Sys().(*syscall.Stat_t).Ino),
	}
	return u, nil
}

func GetNetnsInodeFromFile(fileUrl string) (*url.URL, error) {
	pid, err := convertUrlToPid(fileUrl)
	if err != nil {
		return nil, err
	}
	pidstr := strconv.FormatUint(pid, 10)
	return filenameToURL("/proc/" + pidstr + "/ns/net")
}

func GetAllNetNs() ([]uint64, error) {
	files, err := ioutil.ReadDir("/proc")
	if err != nil {
		return nil, errors.Wrap(err, "can't read /proc directory")
	}
	inodes := make([]uint64, 0, len(files))
	for _, f := range files {
		name := f.Name()
		if isDigits(name) {
			filename := path.Join("/proc", name, "/ns/net")
			inode, err := GetInode(filename)
			if err != nil {
				continue
			}
			inodes = append(inodes, inode)
		}
	}
	return inodes, nil
}

func GetCmdline(pid string) (string, error) {
	data, err := ioutil.ReadFile(path.Join("/proc/", pid, "cmdline"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func GetNetnsFilePath(inodeUrl string) (string, error) {
	inodeNum, err := convertUrlToInode(inodeUrl)
	if err != nil {
		return "", errors.Errorf("failed parsing inode: %s, err: %v", inodeUrl, err)
	}
	/* Get filepath from inode */
	path, err := ResolvePodNsByInode(inodeNum)
	if err != nil {
		return "", errors.Wrapf(err, "failed to find file in /proc/*/ns/net with inode %d", inodeNum)
	}

	return path, nil
}

// GetNsHandleFromInode return namespace handler from inode
func GetNetnsHandleFromURL(inodeUrl string) (netns.NsHandle, error) {
	inodeNum, err := convertUrlToInode(inodeUrl)
	if err != nil {
		return -1, errors.Errorf("failed parsing inode: %s, err: %v", inodeUrl, err)
	}
	/* Get filepath from inode */
	path, err := ResolvePodNsByInode(inodeNum)
	if err != nil {
		return -1, errors.Wrapf(err, "failed to find file in /proc/*/ns/net with inode %d", inodeNum)
	}
	/* Get namespace handler from path */
	return netns.GetFromPath(path)
}

// Current creates net NS handle for the current net NS
func currentNetNs() (handle netns.NsHandle, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	return netns.Get()
}

func convertUrlToInode(inodeUrl string) (uint64, error) {
	urlObj, err := url.Parse(inodeUrl)
	if err != nil {
		return 0, err
	}

	inodeNum := 0
	_, err = fmt.Sscanf(urlObj.Path, "/%d", &inodeNum)
	if err != nil {
		return 0, err
	}

	return uint64(inodeNum), nil
}

func convertUrlToPid(fileUrl string) (uint64, error) {
	urlObj, err := url.Parse(fileUrl)
	if err != nil {
		return 0, err
	}

	pid := 0
	_, err = fmt.Sscanf(urlObj.Path, "/proc/%d/", &pid)
	if err != nil {
		return 0, err
	}

	return uint64(pid), nil
}

// GetNetlinkHandle - mechanism to netlink.Handle for the NetNS specified in mechanism
func GetNetlinkHandleFromURL(inodeUrl string) (*netlink.Handle, error) {
	curNSHandle, err := currentNetNs()
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer func() { _ = curNSHandle.Close() }()

	nsHandle, err := GetNetnsHandleFromURL(inodeUrl)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer func() { _ = nsHandle.Close() }()

	handle, err := netlink.NewHandleAtFrom(nsHandle, curNSHandle)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	return handle, nil
}
