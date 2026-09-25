"""nanicommit バックエンドの入口。

起動: uv run uvicorn app.main:app --reload
"""

import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

import psycopg
from fastapi import FastAPI, Request, status
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from psycopg_pool import PoolTimeout

from app.auth import create_auth_client
from app.config import get_settings
from app.db import create_pool
from app.routers import commits, push, repositories

logger = logging.getLogger(__name__)


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    settings = get_settings()
    app.state.pool = create_pool(settings)
    # DB が起動していなくてもアプリ自体は立ち上げ、各リクエストで 503 を返す。
    await app.state.pool.open(wait=False)
    app.state.auth_client = create_auth_client(settings)
    try:
        yield
    finally:
        await app.state.auth_client.close()
        await app.state.pool.close()


def create_app() -> FastAPI:
    settings = get_settings()
    app = FastAPI(title="nanicommit API", version="0.1.0", lifespan=lifespan)

    app.add_middleware(
        CORSMiddleware,
        allow_origins=settings.cors_origins,
        allow_methods=["GET", "POST"],
        allow_headers=["Authorization", "Content-Type"],
    )

    @app.middleware("http")
    async def limit_request_size(request: Request, call_next):
        # Content-Length で判定する。本文全体を読む前に大きすぎる送信を断る。
        length = request.headers.get("content-length")
        if length is not None and length.isdigit() and int(length) > settings.max_request_bytes:
            return JSONResponse({"detail": "Request too large"}, status.HTTP_413_CONTENT_TOO_LARGE)
        return await call_next(request)

    @app.exception_handler(psycopg.OperationalError)
    @app.exception_handler(PoolTimeout)
    async def database_unavailable(request: Request, exc: Exception) -> JSONResponse:
        logger.error("database unavailable: %s", type(exc).__name__)
        return JSONResponse({"detail": "Database unavailable"}, status.HTTP_503_SERVICE_UNAVAILABLE)

    @app.get("/health", tags=["health"])
    async def health() -> dict[str, str]:
        return {"status": "ok"}

    app.include_router(repositories.router)
    app.include_router(commits.router)
    app.include_router(push.router)
    # Bさん：quizzes.py ができたらここに追加する。
    #   from app.routers import quizzes
    #   app.include_router(quizzes.router)

    return app


app = create_app()
