//
//Copyright 2018-2022 Mailgun Technologies Inc
//
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License.

// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.3.0
// - protoc             (unknown)
// source: gubernator.proto

package gubernator

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

const (
	V1_GetRateLimits_FullMethodName   = "/pb.gubernator.V1/GetRateLimits"
	V1_ClearRateLimits_FullMethodName = "/pb.gubernator.V1/ClearRateLimits"
	V1_HealthCheck_FullMethodName     = "/pb.gubernator.V1/HealthCheck"
)

// V1Client is the client API for V1 service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type V1Client interface {
	// Given a list of rate limit requests, return the rate limits of each.
	GetRateLimits(ctx context.Context, in *GetRateLimitsReq, opts ...grpc.CallOption) (*GetRateLimitsResp, error)
	ClearRateLimits(ctx context.Context, in *ClearRateLimitsReq, opts ...grpc.CallOption) (*ClearRateLimitsResp, error)
	// This method is for round trip benchmarking and can be used by
	// the client to determine connectivity to the server
	HealthCheck(ctx context.Context, in *HealthCheckReq, opts ...grpc.CallOption) (*HealthCheckResp, error)
}

type v1Client struct {
	cc grpc.ClientConnInterface
}

func NewV1Client(cc grpc.ClientConnInterface) V1Client {
	return &v1Client{cc}
}

func (c *v1Client) GetRateLimits(ctx context.Context, in *GetRateLimitsReq, opts ...grpc.CallOption) (*GetRateLimitsResp, error) {
	out := new(GetRateLimitsResp)
	err := c.cc.Invoke(ctx, V1_GetRateLimits_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *v1Client) ClearRateLimits(ctx context.Context, in *ClearRateLimitsReq, opts ...grpc.CallOption) (*ClearRateLimitsResp, error) {
	out := new(ClearRateLimitsResp)
	err := c.cc.Invoke(ctx, V1_ClearRateLimits_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *v1Client) HealthCheck(ctx context.Context, in *HealthCheckReq, opts ...grpc.CallOption) (*HealthCheckResp, error) {
	out := new(HealthCheckResp)
	err := c.cc.Invoke(ctx, V1_HealthCheck_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// V1Server is the server API for V1 service.
// All implementations should embed UnimplementedV1Server
// for forward compatibility
type V1Server interface {
	// Given a list of rate limit requests, return the rate limits of each.
	GetRateLimits(context.Context, *GetRateLimitsReq) (*GetRateLimitsResp, error)
	ClearRateLimits(context.Context, *ClearRateLimitsReq) (*ClearRateLimitsResp, error)
	// This method is for round trip benchmarking and can be used by
	// the client to determine connectivity to the server
	HealthCheck(context.Context, *HealthCheckReq) (*HealthCheckResp, error)
}

// UnimplementedV1Server should be embedded to have forward compatible implementations.
type UnimplementedV1Server struct {
}

func (UnimplementedV1Server) GetRateLimits(context.Context, *GetRateLimitsReq) (*GetRateLimitsResp, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetRateLimits not implemented")
}
func (UnimplementedV1Server) ClearRateLimits(context.Context, *ClearRateLimitsReq) (*ClearRateLimitsResp, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ClearRateLimits not implemented")
}
func (UnimplementedV1Server) HealthCheck(context.Context, *HealthCheckReq) (*HealthCheckResp, error) {
	return nil, status.Errorf(codes.Unimplemented, "method HealthCheck not implemented")
}

// UnsafeV1Server may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to V1Server will
// result in compilation errors.
type UnsafeV1Server interface {
	mustEmbedUnimplementedV1Server()
}

func RegisterV1Server(s grpc.ServiceRegistrar, srv V1Server) {
	s.RegisterService(&V1_ServiceDesc, srv)
}

func _V1_GetRateLimits_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetRateLimitsReq)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(V1Server).GetRateLimits(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: V1_GetRateLimits_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(V1Server).GetRateLimits(ctx, req.(*GetRateLimitsReq))
	}
	return interceptor(ctx, in, info, handler)
}

func _V1_ClearRateLimits_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ClearRateLimitsReq)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(V1Server).ClearRateLimits(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: V1_ClearRateLimits_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(V1Server).ClearRateLimits(ctx, req.(*ClearRateLimitsReq))
	}
	return interceptor(ctx, in, info, handler)
}

func _V1_HealthCheck_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(HealthCheckReq)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(V1Server).HealthCheck(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: V1_HealthCheck_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(V1Server).HealthCheck(ctx, req.(*HealthCheckReq))
	}
	return interceptor(ctx, in, info, handler)
}

// V1_ServiceDesc is the grpc.ServiceDesc for V1 service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var V1_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "pb.gubernator.V1",
	HandlerType: (*V1Server)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "GetRateLimits",
			Handler:    _V1_GetRateLimits_Handler,
		},
		{
			MethodName: "ClearRateLimits",
			Handler:    _V1_ClearRateLimits_Handler,
		},
		{
			MethodName: "HealthCheck",
			Handler:    _V1_HealthCheck_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "gubernator.proto",
}
