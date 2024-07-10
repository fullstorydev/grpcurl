package grpcurl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/golang/protobuf/proto" //lint:ignore SA1019 we have to import this because it appears in exported API
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/desc/protoparse"
	"github.com/jhump/protoreflect/desc/protoprint"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/descriptorpb"
)

// ErrReflectionNotSupported is returned by DescriptorSource operations that
// rely on interacting with the reflection service when the source does not
// actually expose the reflection service. When this occurs, an alternate source
// (like file descriptor sets) must be used.
var ErrReflectionNotSupported = errors.New("server does not support the reflection API")

// DescriptorSource is a source of protobuf descriptor information. It can be backed by a FileDescriptorSet
// proto (like a file generated by protoc) or a remote server that supports the reflection API.
type DescriptorSource interface {
	// ListServices returns a list of fully-qualified service names. It will be all services in a set of
	// descriptor files or the set of all services exposed by a gRPC server.
	ListServices() ([]string, error)
	// FindSymbol returns a descriptor for the given fully-qualified symbol name.
	FindSymbol(fullyQualifiedName string) (desc.Descriptor, error)
	// AllExtensionsForType returns all known extension fields that extend the given message type name.
	AllExtensionsForType(typeName string) ([]*desc.FieldDescriptor, error)
}

// DescriptorSourceFromProtoSets creates a DescriptorSource that is backed by the named files, whose contents
// are encoded FileDescriptorSet protos.
func DescriptorSourceFromProtoSets(fileNames ...string) (DescriptorSource, error) {
	files := &descriptorpb.FileDescriptorSet{}
	for _, fileName := range fileNames {
		b, err := os.ReadFile(fileName)
		if err != nil {
			return nil, fmt.Errorf("could not load protoset file %q: %v", fileName, err)
		}
		var fs descriptorpb.FileDescriptorSet
		err = proto.Unmarshal(b, &fs)
		if err != nil {
			return nil, fmt.Errorf("could not parse contents of protoset file %q: %v", fileName, err)
		}
		files.File = append(files.File, fs.File...)
	}
	return DescriptorSourceFromFileDescriptorSet(files)
}

// DescriptorSourceFromProtoFiles creates a DescriptorSource that is backed by the named files,
// whose contents are Protocol Buffer source files. The given importPaths are used to locate
// any imported files.
func DescriptorSourceFromProtoFiles(importPaths []string, fileNames ...string) (DescriptorSource, error) {
	fileNames, err := protoparse.ResolveFilenames(importPaths, fileNames...)
	if err != nil {
		return nil, err
	}
	p := protoparse.Parser{
		ImportPaths:           importPaths,
		InferImportPaths:      len(importPaths) == 0,
		IncludeSourceCodeInfo: true,
	}
	fds, err := p.ParseFiles(fileNames...)
	if err != nil {
		return nil, fmt.Errorf("could not parse given files: %v", err)
	}
	return DescriptorSourceFromFileDescriptors(fds...)
}

// DescriptorSourceFromFileDescriptorSet creates a DescriptorSource that is backed by the FileDescriptorSet.
func DescriptorSourceFromFileDescriptorSet(files *descriptorpb.FileDescriptorSet) (DescriptorSource, error) {
	unresolved := map[string]*descriptorpb.FileDescriptorProto{}
	for _, fd := range files.File {
		unresolved[fd.GetName()] = fd
	}
	resolved := map[string]*desc.FileDescriptor{}
	for _, fd := range files.File {
		_, err := resolveFileDescriptor(unresolved, resolved, fd.GetName())
		if err != nil {
			return nil, err
		}
	}
	return &fileSource{files: resolved}, nil
}

func resolveFileDescriptor(unresolved map[string]*descriptorpb.FileDescriptorProto, resolved map[string]*desc.FileDescriptor, filename string) (*desc.FileDescriptor, error) {
	if r, ok := resolved[filename]; ok {
		return r, nil
	}
	fd, ok := unresolved[filename]
	if !ok {
		return nil, fmt.Errorf("no descriptor found for %q", filename)
	}
	deps := make([]*desc.FileDescriptor, 0, len(fd.GetDependency()))
	for _, dep := range fd.GetDependency() {
		depFd, err := resolveFileDescriptor(unresolved, resolved, dep)
		if err != nil {
			return nil, err
		}
		deps = append(deps, depFd)
	}
	result, err := desc.CreateFileDescriptor(fd, deps...)
	if err != nil {
		return nil, err
	}
	resolved[filename] = result
	return result, nil
}

// DescriptorSourceFromFileDescriptors creates a DescriptorSource that is backed by the given
// file descriptors
func DescriptorSourceFromFileDescriptors(files ...*desc.FileDescriptor) (DescriptorSource, error) {
	fds := map[string]*desc.FileDescriptor{}
	for _, fd := range files {
		if err := addFile(fd, fds); err != nil {
			return nil, err
		}
	}
	return &fileSource{files: fds}, nil
}

func addFile(fd *desc.FileDescriptor, fds map[string]*desc.FileDescriptor) error {
	name := fd.GetName()
	if existing, ok := fds[name]; ok {
		// already added this file
		if existing != fd {
			// doh! duplicate files provided
			return fmt.Errorf("given files include multiple copies of %q", name)
		}
		return nil
	}
	fds[name] = fd
	for _, dep := range fd.GetDependencies() {
		if err := addFile(dep, fds); err != nil {
			return err
		}
	}
	return nil
}

type fileSource struct {
	files  map[string]*desc.FileDescriptor
	er     *dynamic.ExtensionRegistry
	erInit sync.Once
}

func (fs *fileSource) ListServices() ([]string, error) {
	set := map[string]bool{}
	for _, fd := range fs.files {
		for _, svc := range fd.GetServices() {
			set[svc.GetFullyQualifiedName()] = true
		}
	}
	sl := make([]string, 0, len(set))
	for svc := range set {
		sl = append(sl, svc)
	}
	return sl, nil
}

// GetAllFiles returns all of the underlying file descriptors. This is
// more thorough and more efficient than the fallback strategy used by
// the GetAllFiles package method, for enumerating all files from a
// descriptor source.
func (fs *fileSource) GetAllFiles() ([]*desc.FileDescriptor, error) {
	files := make([]*desc.FileDescriptor, len(fs.files))
	i := 0
	for _, fd := range fs.files {
		files[i] = fd
		i++
	}
	return files, nil
}

func (fs *fileSource) FindSymbol(fullyQualifiedName string) (desc.Descriptor, error) {
	for _, fd := range fs.files {
		if dsc := fd.FindSymbol(fullyQualifiedName); dsc != nil {
			return dsc, nil
		}
	}
	return nil, notFound("Symbol", fullyQualifiedName)
}

func (fs *fileSource) AllExtensionsForType(typeName string) ([]*desc.FieldDescriptor, error) {
	fs.erInit.Do(func() {
		fs.er = &dynamic.ExtensionRegistry{}
		for _, fd := range fs.files {
			fs.er.AddExtensionsFromFile(fd)
		}
	})
	return fs.er.AllExtensionsForType(typeName), nil
}

// DescriptorSourceFromServer creates a DescriptorSource that uses the given gRPC reflection client
// to interrogate a server for descriptor information. If the server does not support the reflection
// API then the various DescriptorSource methods will return ErrReflectionNotSupported
func DescriptorSourceFromServer(_ context.Context, refClient *grpcreflect.Client) DescriptorSource {
	return serverSource{client: refClient}
}

type serverSource struct {
	client *grpcreflect.Client
}

func (ss serverSource) ListServices() ([]string, error) {
	svcs, err := ss.client.ListServices()
	return svcs, reflectionSupport(err)
}

func (ss serverSource) FindSymbol(fullyQualifiedName string) (desc.Descriptor, error) {
	file, err := ss.client.FileContainingSymbol(fullyQualifiedName)
	if err != nil {
		return nil, reflectionSupport(err)
	}
	d := file.FindSymbol(fullyQualifiedName)
	if d == nil {
		return nil, notFound("Symbol", fullyQualifiedName)
	}
	return d, nil
}

func (ss serverSource) AllExtensionsForType(typeName string) ([]*desc.FieldDescriptor, error) {
	var exts []*desc.FieldDescriptor
	nums, err := ss.client.AllExtensionNumbersForType(typeName)
	if err != nil {
		return nil, reflectionSupport(err)
	}
	for _, fieldNum := range nums {
		ext, err := ss.client.ResolveExtension(typeName, fieldNum)
		if err != nil {
			return nil, reflectionSupport(err)
		}
		exts = append(exts, ext)
	}
	return exts, nil
}

func reflectionSupport(err error) error {
	if err == nil {
		return nil
	}
	if stat, ok := status.FromError(err); ok && stat.Code() == codes.Unimplemented {
		return ErrReflectionNotSupported
	}
	return err
}

// WriteProtoset will use the given descriptor source to resolve all of the given
// symbols and write a proto file descriptor set with their definitions to the
// given output. The output will include descriptors for all files in which the
// symbols are defined as well as their transitive dependencies.
func WriteProtoset(out io.Writer, descSource DescriptorSource, symbols ...string) error {
	filenames, fds, err := getFileDescriptors(symbols, descSource)
	if err != nil {
		return err
	}
	// now expand that to include transitive dependencies in topologically sorted
	// order (such that file always appears after its dependencies)
	expandedFiles := make(map[string]struct{}, len(fds))
	allFilesSlice := make([]*descriptorpb.FileDescriptorProto, 0, len(fds))
	for _, filename := range filenames {
		allFilesSlice = addFilesToSet(allFilesSlice, expandedFiles, fds[filename])
	}
	// now we can serialize to file
	b, err := proto.Marshal(&descriptorpb.FileDescriptorSet{File: allFilesSlice})
	if err != nil {
		return fmt.Errorf("failed to serialize file descriptor set: %v", err)
	}
	if _, err := out.Write(b); err != nil {
		return fmt.Errorf("failed to write file descriptor set: %v", err)
	}
	return nil
}

func addFilesToSet(allFiles []*descriptorpb.FileDescriptorProto, expanded map[string]struct{}, fd *desc.FileDescriptor) []*descriptorpb.FileDescriptorProto {
	if _, ok := expanded[fd.GetName()]; ok {
		// already seen this one
		return allFiles
	}
	expanded[fd.GetName()] = struct{}{}
	// add all dependencies first
	for _, dep := range fd.GetDependencies() {
		allFiles = addFilesToSet(allFiles, expanded, dep)
	}
	return append(allFiles, fd.AsFileDescriptorProto())
}

// WriteProtoFiles will use the given descriptor source to resolve all the given
// symbols and write proto files with their definitions to the given output directory.
func WriteProtoFiles(outProtoDirPath string, descSource DescriptorSource, symbols ...string) error {
	filenames, fds, err := getFileDescriptors(symbols, descSource)
	if err != nil {
		return err
	}
	// now expand that to include transitive dependencies in topologically sorted
	// order (such that file always appears after its dependencies)
	expandedFiles := make(map[string]struct{}, len(fds))
	allFilesSlice := make([]*desc.FileDescriptor, 0, len(fds))
	for _, filename := range filenames {
		allFilesSlice = addFilesToFileDescriptorList(allFilesSlice, expandedFiles, fds[filename])
	}
	pr := protoprint.Printer{}
	// now we can serialize to files
	for _, fd := range allFilesSlice {
		fdFQName := fd.GetFullyQualifiedName()
		dirPath := filepath.Dir(fdFQName)
		outFilepath := filepath.Join(outProtoDirPath, dirPath)
		if err := os.MkdirAll(outFilepath, 0755); err != nil {
			return fmt.Errorf("failed to create directory %q: %v", outFilepath, err)
		}
		fileName := filepath.Base(fdFQName)
		filePath := filepath.Join(outFilepath, fileName)
		f, err := os.Create(filePath)
		if err != nil {
			return fmt.Errorf("failed to create file %q: %v", filePath, err)
		}
		err = pr.PrintProtoFile(fd, f)
		if err == nil {
			_ = f.Close()
		} else {
			return fmt.Errorf("failed to write file %q: %v", filePath, err)
		}
	}
	return nil
}

func getFileDescriptors(symbols []string, descSource DescriptorSource) ([]string, map[string]*desc.FileDescriptor, error) {
	// compute set of file descriptors
	filenames := make([]string, 0, len(symbols))
	fds := make(map[string]*desc.FileDescriptor, len(symbols))
	for _, sym := range symbols {
		d, err := descSource.FindSymbol(sym)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to find descriptor for %q: %v", sym, err)
		}
		fd := d.GetFile()
		if _, ok := fds[fd.GetName()]; !ok {
			fds[fd.GetName()] = fd
			filenames = append(filenames, fd.GetName())
		}
	}
	return filenames, fds, nil
}

func addFilesToFileDescriptorList(allFiles []*desc.FileDescriptor, expanded map[string]struct{}, fd *desc.FileDescriptor) []*desc.FileDescriptor {
	if _, ok := expanded[fd.GetName()]; ok {
		// already seen this one
		return allFiles
	}
	expanded[fd.GetName()] = struct{}{}
	// add all dependencies first
	for _, dep := range fd.GetDependencies() {
		allFiles = addFilesToFileDescriptorList(allFiles, expanded, dep)
	}
	return append(allFiles, fd)
}
