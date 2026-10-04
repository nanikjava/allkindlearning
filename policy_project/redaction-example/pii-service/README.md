This directory contains experiment for building pii-service using different models:

# Gemma

The `gemma` directory contains a Python FastAPI application that interface with Gemma3/4 in `GGUF` format that run locally using llama.

The experience of using gemma model based on prompt in `test_prompt` directory are slow and not suitable to perform PII checking for synchronous process, but for asynchronous process still reasonable.

The output from the model is very good in terms of detecting PII information.

# Gliner2

The `gliner2` directory contains a Python that loads the model `fastino/gliner2-privacy-filter-PII-multi`. The application provide `/redact` and `/detect` endpoint accepting text that will be ran through the model to detect PII information. The different PII thresholds and labels are specified so that model can run through the detection correctly.

For small amount of data less than 5KB the process ranges from 100-300ms, more than that it slows down dramatically.

The output is specific to the model it cannot output what we want, it has it's own output format. PII detection is good enough for application but lower than Gemma

# Spacy

The spacy model is fast in terms of processing information. All the `test_prompt` test data goes through the pipeline in less than 2-3seconds higher than both the above.

In terms of accuracy the built-in recognizer library are quite solid, but there are gaps when it comes to country specific PII. For example - it cannot detect drive license for AU, so need to create `au_license_recognizer.py` so it can detect AU driver license.

# Impression

If hardware is not a constraint and speed does not matter then Gemma is the best. But if speed matters than Spacy is the clear winner.

There was testing done with other PII specific models such as:

- `QuantFactory/Llama-Guard-3-1B-GGUF`
- `LiquidAI/LFM2.5-1.2B-Instruct-GGUF`

The result is equivalent to gliner2 and sometime close to Gemma, though `llama-guard` has it's own output format.
