#include <zhulong/algo.hpp>

class MockPersonStructurePlugin {
public:
    zhulong_status_t init(const std::string& config_json) {
        (void)config_json;
        model_ = zhulong::Model::load("mock_person_structure.weights");
        if (!model_.valid()) {
            return ZHULONG_PLUGIN_ERR_INTERNAL;
        }
        return ZHULONG_PLUGIN_OK;
    }

    zhulong::Session create_session(uint32_t worker_id) {
        return model_.create_session(worker_id);
    }

    std::vector<zhulong::PersonItem> detect_persons(zhulong::Session& session, const zhulong::Image& image) {
        (void)image;
        auto outputs = session.forward({});

        zhulong::PersonItem person;
        person.track_id = 1001;
        person.body_box[0] = 0.10f;
        person.body_box[1] = 0.15f;
        person.body_box[2] = 0.50f;
        person.body_box[3] = 0.85f;
        person.body_score = 0.95f;

        zhulong::FaceItem face;
        face.box[0] = 0.15f;
        face.box[1] = 0.20f;
        face.box[2] = 0.35f;
        face.box[3] = 0.40f;
        face.quality = 0.88f;
        face.matched = true;
        face.similarity = 0.92f;
        face.is_fused = false;
        face.person_id = "user_007";
        face.person_name = "Alice";
        face.matched_face_id = "face_007";

        face.landmarks = {
            {0.20f, 0.25f},
            {0.30f, 0.25f},
            {0.25f, 0.30f},
            {0.22f, 0.35f},
            {0.28f, 0.35f}
        };

        zhulong::FaceCandidate cand1;
        cand1.person_id = "user_007";
        cand1.person_name = "Alice";
        cand1.face_id = "face_007";
        cand1.similarity = 0.92f;

        zhulong::FaceCandidate cand2;
        cand2.person_id = "user_008";
        cand2.person_name = "Bob";
        cand2.face_id = "face_008";
        cand2.similarity = 0.45f;

        face.candidates = {cand1, cand2};
        person.faces.push_back(face);

        return {person};
    }

private:
    zhulong::Model model_;
};

ZHULONG_REGISTER_PERSON_STRUCTURE_PLUGIN(MockPersonStructurePlugin, "MockPersonStructure", "1.0.0")
