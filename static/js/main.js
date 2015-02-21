$(document).init(function() {
    var conn = new WebSocket('ws://' + location.host + '/nodez/wire/stream');

    conn.onmessage = handleWireStreamMessage
});


function handleWireStreamMessage(e) {
    d = $.parseJSON(e.data)

    console.log(d)

    switch (d["Command"]) {
    case "inv":
        for (var i = 0; i < d["Inv"].length; i++) {
            item = d["Inv"][i]
            el = $("#messageStream")
                .prepend("<div class='message-stream-item'>" + item["Hash"] + "</div>");
        }

        // Prune messages to avoid using too much memory. CSS overflow is used
        // for styling.
        var items = $("#messageStream .message-stream-item");
        for (var i = items.length; i > 500; i--) {
            items.eq(i).remove();
        }
        break;
    }
}
