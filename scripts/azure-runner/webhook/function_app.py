import logging

import azure.functions as func

from runner_start import handle

app = func.FunctionApp(http_auth_level=func.AuthLevel.ANONYMOUS)


@app.route(route="github", methods=["POST"])
def github(req: func.HttpRequest) -> func.HttpResponse:
    headers = {k.lower(): v for k, v in req.headers.items()}
    status, message = handle(headers, req.get_body())
    logging.info("delivery=%s event=%s result=%s %s",
                 headers.get("x-github-delivery"), headers.get("x-github-event"), status, message)
    return func.HttpResponse(message, status_code=status)
