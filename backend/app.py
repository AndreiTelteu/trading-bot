from flask import Flask, send_from_directory
from flask_cors import CORS
from flask_socketio import SocketIO
from apscheduler.schedulers.background import BackgroundScheduler
from backend.config import Config
from backend.models import db
from backend.database import init_db
from backend.routes.api import api
from backend.routes.settings import settings_bp
from backend.routes.websocket import init_websocket
import os

app = Flask(__name__, static_folder="../frontend/dist", static_url_path="")
app.config.from_object(Config)

CORS(app, resources={r"/api/*": {"origins": "*"}})

socketio = SocketIO(app, cors_allowed_origins="*", ping_interval=25, ping_timeout=60)

db.init_app(app)

app.register_blueprint(api, url_prefix="/api")
app.register_blueprint(settings_bp, url_prefix="/api")

init_websocket(socketio)

with app.app_context():
    init_db(app)


@app.route("/")
def index():
    return send_from_directory(app.static_folder, "index.html")


@app.route("/<path:path>")
def serve_static(path):
    if os.path.exists(os.path.join(app.static_folder, path)):
        return send_from_directory(app.static_folder, path)
    return send_from_directory(app.static_folder, "index.html")


if __name__ == "__main__":
    port = int(os.environ.get("PORT", 5001))
    socketio.run(app, host="0.0.0.0", port=port, debug=True)
