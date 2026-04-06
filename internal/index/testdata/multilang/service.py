import os
from typing import Optional

MAX_RETRIES = 3

class UserService:
    def __init__(self, db_url: str):
        self.db_url = db_url

    def get_user(self, user_id: str) -> Optional[dict]:
        return {"id": user_id, "name": "test"}

    def create_user(self, name: str, email: str) -> dict:
        return {"name": name, "email": email}

def connect_db(url: str) -> None:
    pass

_internal_helper = "private"
