package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>
*/
import "C"

// brokerPathBufferBytes 是 FrameBroker socket 路径的接收缓冲。
// Unix domain socket 路径长度受 sockaddr_un.sun_path 限制（Linux 108 字节），
// 256 留出足够余量；超出时原生侧会返回 ERR_BUFFER_TOO_SMALL。
const brokerPathBufferBytes = 256

func (e *Engine) getBrokerPath(gen uint64, streamID C.Zhulong_stream_id) (string, error) {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return "", err
	}
	defer release()

	var buf [brokerPathBufferBytes]C.char
	inoutLen := C.size_t(len(buf))
	st := C.Zhulong_stream_get_broker_path(lease.handle, streamID, &buf[0], &inoutLen)
	if st != C.Zhulong_OK {
		return "", mapNativeStatus("get_broker_path", int32(st))
	}
	return C.GoString(&buf[0]), nil
}

// brokerClientsCapacity 是单次快照最多取回的客户端条数。
//
// 一个物理流上同时订阅的算法包量以个位数计，16 已经留足余量；多出的部分不会丢，
// 而是体现在 out_count 上（调用方拿 total > len(clients) 就能发现）。
const brokerClientsCapacity = 16

func (e *Engine) getBrokerClients(gen uint64, streamID C.Zhulong_stream_id) ([]BrokerClientStats, uint32, error) {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return nil, 0, err
	}
	defer release()

	var buf [brokerClientsCapacity]C.Zhulong_broker_client_stats
	var total C.uint32_t
	st := C.Zhulong_stream_get_broker_clients(
		lease.handle, streamID, &buf[0], C.uint32_t(len(buf)), &total)
	if st != C.Zhulong_OK {
		return nil, 0, mapNativeStatus("get_broker_clients", int32(st))
	}

	written := min(int(total), len(buf))
	clients := make([]BrokerClientStats, written)
	for i := range written {
		clients[i] = BrokerClientStats{
			ClientID:        uint32(buf[i].client_id),
			Name:            C.GoString(&buf[i].name[0]),
			InFlight:        uint32(buf[i].in_flight),
			MaxInFlight:     uint32(buf[i].max_in_flight),
			DeliveredFrames: uint64(buf[i].delivered_frames),
			DroppedFrames:   uint64(buf[i].dropped_frames),
		}
	}
	return clients, uint32(total), nil
}

func (e *Engine) setPublishRate(gen uint64, streamID C.Zhulong_stream_id, fps uint32) error {
	lease, release, err := e.borrowHandle(handleRequirement{
		requireRunning:    true,
		requireGeneration: &gen,
		deniedError:       ErrNotRunning,
	})
	if err != nil {
		return err
	}
	defer release()

	st := C.Zhulong_stream_set_publish_rate(lease.handle, streamID, C.uint32_t(fps))
	if st != C.Zhulong_OK {
		return mapNativeStatus("set_publish_rate", int32(st))
	}
	return nil
}
