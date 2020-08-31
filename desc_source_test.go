package grpcurl

import (
	"bytes"
	"io/ioutil"
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
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

	mergedProtoset := &descriptor.FileDescriptorSet{
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

func loadProtoset(path string) (*descriptor.FileDescriptorSet, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var protoset descriptor.FileDescriptorSet
	if err := proto.Unmarshal(b, &protoset); err != nil {
		return nil, err
	}
	return &protoset, nil
}

func checkWriteProtoset(t *testing.T, descSrc DescriptorSource, protoset *descriptor.FileDescriptorSet, symbols ...string) {
	var buf bytes.Buffer
	if err := WriteProtoset(&buf, descSrc, symbols...); err != nil {
		t.Fatalf("failed to write protoset: %v", err)
	}

	var result descriptor.FileDescriptorSet
	if err := proto.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("failed to unmarshal written protoset: %v", err)
	}

	if !proto.Equal(protoset, &result) {
		t.Fatalf("written protoset not equal to input:\nExpecting: %s\nActual: %s", protoset, &result)
	}
}
