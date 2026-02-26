from flask import Blueprint, jsonify, request
from backend.models import db, Setting, IndicatorWeight, LLMConfig, AIProposal
from datetime import datetime

settings_bp = Blueprint("settings", __name__)


@settings_bp.route("/settings", methods=["GET"])
def get_settings():
    settings = Setting.query.all()
    return jsonify({s.key: s.value for s in settings})


@settings_bp.route("/settings", methods=["PUT"])
def update_settings():
    data = request.json
    for key, value in data.items():
        setting = Setting.query.get(key)
        if setting:
            setting.value = str(value)
            setting.updated_at = datetime.utcnow()
        else:
            setting = Setting(key=key, value=str(value))
            db.session.add(setting)
    db.session.commit()
    return jsonify({"success": True})


@settings_bp.route("/settings/<key>", methods=["GET"])
def get_setting(key):
    setting = Setting.query.get(key)
    if setting:
        return jsonify({"key": key, "value": setting.value})
    return jsonify({"error": "Setting not found"}), 404


@settings_bp.route("/settings/category/<category>", methods=["GET"])
def get_settings_by_category(category):
    settings = Setting.query.filter_by(category=category).all()
    return jsonify({s.key: s.value for s in settings})


@settings_bp.route("/indicator-weights", methods=["GET"])
def get_indicator_weights():
    weights = IndicatorWeight.query.all()
    return jsonify({w.indicator: w.weight for w in weights})


@settings_bp.route("/indicator-weights", methods=["PUT"])
def update_indicator_weights():
    data = request.json
    for indicator, weight in data.items():
        iw = IndicatorWeight.query.get(indicator)
        if iw:
            iw.weight = weight
        else:
            iw = IndicatorWeight(indicator=indicator, weight=weight)
            db.session.add(iw)
    db.session.commit()
    return jsonify({"success": True})


@settings_bp.route("/llm/config", methods=["GET"])
def get_llm_config():
    config = LLMConfig.query.first()
    if not config:
        config = LLMConfig()
        db.session.add(config)
        db.session.commit()

    return jsonify(
        {
            "provider": config.provider,
            "base_url": config.base_url,
            "api_key": config.api_key if config.api_key else "",
            "model": config.model,
        }
    )


@settings_bp.route("/llm/config", methods=["PUT"])
def update_llm_config():
    data = request.json
    config = LLMConfig.query.first()
    if not config:
        config = LLMConfig()
        db.session.add(config)

    if "provider" in data:
        config.provider = data["provider"]
    if "base_url" in data:
        config.base_url = data["base_url"]
    if "api_key" in data:
        config.api_key = data["api_key"]
    if "model" in data:
        config.model = data["model"]

    config.updated_at = datetime.utcnow()
    db.session.commit()
    return jsonify({"success": True})


@settings_bp.route("/llm/test", methods=["POST"])
def test_llm_connection():
    return jsonify(
        {
            "success": True,
            "message": "LLM configuration saved. Actual connection test requires API key.",
        }
    )


@settings_bp.route("/ai/proposals", methods=["GET"])
def get_ai_proposals():
    status = request.args.get("status")
    query = AIProposal.query
    if status:
        query = query.filter_by(status=status)
    proposals = query.order_by(AIProposal.created_at.desc()).all()
    return jsonify(
        [
            {
                "id": p.id,
                "proposal_type": p.proposal_type,
                "parameter_key": p.parameter_key,
                "old_value": p.old_value,
                "new_value": p.new_value,
                "reasoning": p.reasoning,
                "status": p.status,
                "created_at": p.created_at.isoformat() if p.created_at else None,
                "resolved_at": p.resolved_at.isoformat() if p.resolved_at else None,
            }
            for p in proposals
        ]
    )


@settings_bp.route("/ai/proposals", methods=["POST"])
def create_ai_proposal():
    data = request.json
    proposal = AIProposal(
        proposal_type=data.get("proposal_type"),
        parameter_key=data.get("parameter_key"),
        old_value=data.get("old_value"),
        new_value=data.get("new_value"),
        reasoning=data.get("reasoning"),
        status="pending",
    )
    db.session.add(proposal)
    db.session.commit()
    return jsonify({"id": proposal.id})


@settings_bp.route("/ai/proposals/<int:proposal_id>/approve", methods=["POST"])
def approve_proposal(proposal_id):
    proposal = AIProposal.query.get(proposal_id)
    if not proposal:
        return jsonify({"error": "Proposal not found"}), 404

    if proposal.status != "pending":
        return jsonify({"error": "Proposal already resolved"}), 400

    proposal.status = "approved"
    proposal.resolved_at = datetime.utcnow()

    if proposal.parameter_key:
        setting = Setting.query.get(proposal.parameter_key)
        if setting:
            setting.value = proposal.new_value
            setting.updated_at = datetime.utcnow()

    db.session.commit()
    return jsonify({"success": True})


@settings_bp.route("/ai/proposals/<int:proposal_id>/deny", methods=["POST"])
def deny_proposal(proposal_id):
    proposal = AIProposal.query.get(proposal_id)
    if not proposal:
        return jsonify({"error": "Proposal not found"}), 404

    if proposal.status != "pending":
        return jsonify({"error": "Proposal already resolved"}), 400

    proposal.status = "denied"
    proposal.resolved_at = datetime.utcnow()
    db.session.commit()

    return jsonify({"success": True})


@settings_bp.route("/ai/generate-proposals", methods=["POST"])
def generate_ai_proposals():
    from backend.services.ai_service import generate_proposals

    try:
        proposals = generate_proposals()
        return jsonify({"proposals": proposals})
    except Exception as e:
        return jsonify({"error": str(e)}), 500
