---
id: failures/model_content_flagged
title: model_content_flagged
summary: The model provider's content filter blocked the request, so the task did not finish.
link: /settings/models#providers
kind: failure
---
# model_content_flagged

Open this page: [Where to look](/settings/models#providers)

**Meaning.** The model provider's content filter blocked the request or the model's answer, so the task did not finish. Completed tool results are kept.

**Usual cause.** The provider's safety classifier flagged the request. Classifiers also flag harmless requests, for example an image from a video. For an OpenAI or Codex prompt flag, TOFI already sent the request again once; the card says when that retry was blocked too.

**Steps.**
1. Retry the task from its card.
2. If it is blocked again, rephrase the request or leave out the part that may have triggered the filter.
3. If it keeps happening with one model, choose a model from another provider.
