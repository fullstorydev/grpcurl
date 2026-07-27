package grpcurl

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestWriteProtoset(t *testing.T) {
	exampleProtoset, err := loadProtoset("./internal/testing/example.protoset")
	if err != nil {
		t.Fatalf("failed to load example.protoset: %v", err)
	}
	testProtoset, err := loadProtoset("./internal/testing/test.protoset")
	if err != nil {
		t.Fatalf("failed to load test.protoset: %v", err)
	}

	mergedProtoset := &descriptorpb.FileDescriptorSet{
		File: append(exampleProtoset.File, testProtoset.File...),
	}

	descSrc, err := DescriptorSourceFromFileDescriptorSet(mergedProtoset)
	if err != nil {
		t.Fatalf("failed to create descriptor source: %v", err)
	}

	checkWriteProtoset(t, descSrc, exampleProtoset, "TestService")
	checkWriteProtoset(t, descSrc, testProtoset, "testing.TestService")
	checkWriteProtoset(t, descSrc, mergedProtoset, "TestService", "testing.TestService")
}

func loadProtoset(path string) (*descriptorpb.FileDescriptorSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var protoset descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(b, &protoset); err != nil {
		return nil, err
	}
	return &protoset, nil
}

func checkWriteProtoset(t *testing.T, descSrc DescriptorSource, protoset *descriptorpb.FileDescriptorSet, symbols ...string) {
	var buf bytes.Buffer
	if err := WriteProtoset(&buf, descSrc, symbols...); err != nil {
		t.Fatalf("failed to write protoset: %v", err)
	}

	var result descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("failed to unmarshal written protoset: %v", err)
	}

	if !proto.Equal(protoset, &result) {
		t.Fatalf("written protoset not equal to input:\nExpecting: %s\nActual: %s", protoset, &result)
	}
}

func writeProtoFileForTest(t *testing.T, dir, fdName string) error {
	t.Helper()
	fd, err := desc.CreateFileDescriptor(&descriptorpb.FileDescriptorProto{
		Name:   proto.String(fdName),
		Syntax: proto.String("proto3"),
	})
	if err != nil {
		t.Fatalf("failed to create file descriptor: %v", err)
	}
	return writeProtoFiles(dir, []*desc.FileDescriptor{fd})
}

func TestWriteProtoFile_NormalPath(t *testing.T) {
	dir := t.TempDir()
	if err := writeProtoFileForTest(t, dir, "foo/bar.proto"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "foo", "bar.proto")); err != nil {
		t.Fatalf("expected output file not created: %v", err)
	}
}

func TestWriteProtoFile_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := writeProtoFileForTest(t, dir, "../escape.proto"); err == nil {
		t.Fatal("expected error for path-traversing descriptor name, got nil")
	}
	escapePath := filepath.Join(dir, "..", "escape.proto")
	if _, err := os.Stat(escapePath); err == nil {
		t.Fatalf("file was created outside output directory at %q", escapePath)
	}
}

func TestWriteProtoFile_RejectsDeepPathTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := writeProtoFileForTest(t, dir, "foo/../../../escape.proto"); err == nil {
		t.Fatal("expected error for path-traversing descriptor name, got nil")
	}
	escapePath := filepath.Join(dir, "foo", "..", "..", "..", "escape.proto")
	if _, err := os.Stat(escapePath); err == nil {
		t.Fatalf("file was created outside output directory at %q", escapePath)
	}
}
