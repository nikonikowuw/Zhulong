#include "Zhulong/engine.h"

#include <mutex>
#include <new>

struct Zhulong_engine_t
{
    enum class State
    {
        created,
        running,
        stopped
    };

    std::mutex mutex;
    State state = State::created;
};

extern "C" Zhulong_status_t Zhulong_engine_create(Zhulong_engine_h *out_engine)
{
    if (out_engine == nullptr)
    {
        return Zhulong_ERR_INVALID_ARGUMENT;
    }

    *out_engine = nullptr;
    try
    {
        auto *engine = new Zhulong_engine_t();
        *out_engine = engine;
        return Zhulong_OK;
    }
    catch (const std::bad_alloc &)
    {
        return Zhulong_ERR_OUT_OF_MEMORY;
    }
    catch (...)
    {
        return Zhulong_ERR_INTERNAL;
    }
}

extern "C" Zhulong_status_t Zhulong_engine_start(Zhulong_engine_h engine)
{
    if (engine == nullptr)
    {
        return Zhulong_ERR_INVALID_ARGUMENT;
    }

    try
    {
        std::lock_guard<std::mutex> lock(engine->mutex);
        engine->state = Zhulong_engine_t::State::running;
        return Zhulong_OK;
    }
    catch (...)
    {
        return Zhulong_ERR_INTERNAL;
    }
}

extern "C" Zhulong_status_t Zhulong_engine_stop(Zhulong_engine_h engine)
{
    if (engine == nullptr)
    {
        return Zhulong_ERR_INVALID_ARGUMENT;
    }

    try
    {
        std::lock_guard<std::mutex> lock(engine->mutex);
        engine->state = Zhulong_engine_t::State::stopped;
        return Zhulong_OK;
    }
    catch (...)
    {
        return Zhulong_ERR_INTERNAL;
    }
}

extern "C" void Zhulong_engine_destroy(Zhulong_engine_h engine)
{
    try
    {
        delete engine;
    }
    catch (...)
    {
    }
}
