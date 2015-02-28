$(document).init(function() {
    var wire = new WebSocket('ws://' + location.host + '/nodez/wire/stream');
    var info = new WebSocket('ws://' + location.host + '/nodez/info/stream');

    wire.onmessage = handleWireStreamMessage
    info.onmessage = handleWireStreamMessage
});


function handleWireStreamMessage(e) {
    d = $.parseJSON(e.data)

    console.log(d)

    switch (d["Command"]) {
    case "sync":
        item = d["Sync"]
        el = $("#messageStream").prepend("<div class='message-stream-item'>" + item["Hash"] + "</div>");

    case "inv":
        for (var i = 0; i < d["Inv"].length; i++) {
            item = d["Inv"][i]
            el = $("#messageStream").prepend("<div class='message-stream-item'>" + item["Hash"] + "</div>");
        }
        break;
    }
    // Prune messages to avoid using too much memory. CSS overflow is used
    // for styling.
    var items = $("#messageStream .message-stream-item");
    for (var i = items.length; i > 500; i--) {
        items.eq(i).remove();
    }
}

function handleInfoStreamMessage(e) {
    d = $.parseJSON(e.data)

    console.log("info: ", d)
}
