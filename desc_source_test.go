package grpcurl

import (
	"bytes"
	"os"
	"testing"

	"github.com/golang/protobuf/proto" //lint:ignore SA1019 we have to import this because it appears in exported API
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
